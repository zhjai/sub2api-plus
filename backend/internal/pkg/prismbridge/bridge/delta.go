// Copyright (c) oai-prism contributors. MIT; see LICENSE.
// Port of facade/toolcall.go and toolbridge.go, pinned a97dbdc.
package bridge

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Wei-Shaw/sub2api/internal/pkg/prismbridge/prism"
	"path"
	"path/filepath"
	"strconv"
	"strings"
)

type fileEdit struct {
	Path    string     // 已校验的相对路径（正斜杠分隔）
	Kind    string     // editWrite | editPatch | editDelete
	Content string     // editWrite：完整文件内容
	Hunks   []editHunk // editPatch：按顺序应用的替换块
}
type editHunk struct {
	Old  string
	New  string
	Line int
}

const (
	editWrite  = "write"
	editPatch  = "patch"
	editDelete = "delete"
)

var errIgnoredFile = errors.New("system file excluded")

func safeRelPath(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", errors.New("空路径")
	}
	for _, r := range p {
		if r < 0x20 || r == 0x7f {
			return "", fmt.Errorf("路径含控制字符: %q", p)
		}
	}
	p = strings.ReplaceAll(p, "\\", "/")
	if strings.HasPrefix(p, "/") || strings.Contains(p, ":") {
		return "", fmt.Errorf("拒绝绝对路径: %s", p)
	}
	clean := path.Clean(p)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("路径越出工作区: %s", p)
	}
	return clean, nil
}
func planDeltaFile(f prism.CodexDeltaFile) (fileEdit, error) {
	if isSystemIgnoredFile(f.FilePath) {
		return fileEdit{}, errIgnoredFile
	}
	rel, err := safeRelPath(f.FilePath)
	if err != nil {
		return fileEdit{}, err
	}
	if f.Status == "deleted" {
		return fileEdit{Path: rel, Kind: editDelete}, nil
	}

	hunks, err := parseDeltaHunks(f.Diff)
	if err != nil {
		return fileEdit{}, err
	}
	if len(hunks) == 0 {
		return fileEdit{}, errors.New("diff 为空")
	}

	// 整份内容：全部是纯插入且从文件开头起（新增文件的形态）。
	whole := true
	for _, h := range hunks {
		if h.Old != "" || h.Line != 0 {
			whole = false
			break
		}
	}
	if whole && f.Status == "added" {
		var sb strings.Builder
		for _, h := range hunks {
			sb.WriteString(h.New)
		}
		return fileEdit{Path: rel, Kind: editWrite, Content: sb.String()}, nil
	}
	if f.Status == "added" {
		return fileEdit{}, errors.New("新增文件的 diff 不含完整内容")
	}
	return fileEdit{Path: rel, Kind: editPatch, Hunks: hunks}, nil
}
func parseDeltaHunks(raw json.RawMessage) ([]editHunk, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return parseUnifiedDiff(s)
	}
	var obj struct {
		Hunks []struct {
			Original string `json:"original"`
			Updated  string `json:"updated"`
			Location struct {
				OriginalStartLine int `json:"originalStartLine"`
				OriginalLineCount int `json:"originalLineCount"`
			} `json:"location"`
		} `json:"hunks"`
	}
	if err := json.Unmarshal(raw, &obj); err != nil || obj.Hunks == nil {
		return nil, errors.New("无法识别的 diff 形态")
	}
	out := make([]editHunk, 0, len(obj.Hunks))
	for _, h := range obj.Hunks {
		line := h.Location.OriginalStartLine
		if h.Original == "" && h.Location.OriginalLineCount == 0 {
			// 纯插入：originalStartLine 表示"插在这一行之后"。
			out = append(out, editHunk{New: h.Updated, Line: line})
			continue
		}
		out = append(out, editHunk{Old: h.Original, New: h.Updated, Line: line})
	}
	return out, nil
}
func parseUnifiedDiff(diff string) ([]editHunk, error) {
	lines := strings.Split(strings.ReplaceAll(diff, "\r\n", "\n"), "\n")
	var (
		out            []editHunk
		inHunk         bool
		oldStart       int
		oldCount       int
		remOld, remNew int
		lastKind       byte
		oldBuf, newBuf strings.Builder
	)
	flush := func() {
		if !inHunk {
			return
		}
		h := editHunk{Old: oldBuf.String(), New: newBuf.String(), Line: oldStart}
		if oldCount == 0 {
			h.Old = "" // 纯插入：插在 oldStart 行之后
		}
		out = append(out, h)
		inHunk = false
		oldBuf.Reset()
		newBuf.Reset()
	}
	trimLast := func(s string) string { return strings.TrimSuffix(s, "\n") }

	for _, line := range lines {
		if strings.HasPrefix(line, "@@") {
			flush()
			var newCount int
			var err error
			oldStart, oldCount, newCount, err = parseHunkHeader(line)
			if err != nil {
				return nil, err
			}
			remOld, remNew = oldCount, newCount
			inHunk, lastKind = true, 0
			continue
		}
		// "\ No newline at end of file"：去掉上一行补上的换行（可能紧跟在已结束的 hunk 之后）。
		if strings.HasPrefix(line, `\`) {
			if inHunk {
				o, n := oldBuf.String(), newBuf.String()
				if lastKind == '-' || lastKind == ' ' {
					o = trimLast(o)
				}
				if lastKind == '+' || lastKind == ' ' {
					n = trimLast(n)
				}
				oldBuf.Reset()
				oldBuf.WriteString(o)
				newBuf.Reset()
				newBuf.WriteString(n)
			} else if len(out) > 0 {
				h := &out[len(out)-1]
				if lastKind == '-' || lastKind == ' ' {
					h.Old = trimLast(h.Old)
				}
				if lastKind == '+' || lastKind == ' ' {
					h.New = trimLast(h.New)
				}
			}
			continue
		}
		if !inHunk {
			continue // 文件头（diff --git / index / --- / +++）或 hunk 之间的杂项
		}
		switch {
		case strings.HasPrefix(line, "-") && remOld > 0:
			oldBuf.WriteString(line[1:] + "\n")
			remOld--
			lastKind = '-'
		case strings.HasPrefix(line, "+") && remNew > 0:
			newBuf.WriteString(line[1:] + "\n")
			remNew--
			lastKind = '+'
		case (strings.HasPrefix(line, " ") || line == "") && remOld > 0 && remNew > 0:
			// 有些生成器会把空上下文行的前导空格吃掉。
			ctx := strings.TrimPrefix(line, " ")
			oldBuf.WriteString(ctx + "\n")
			newBuf.WriteString(ctx + "\n")
			remOld--
			remNew--
			lastKind = ' '
		default:
			if line == "" {
				continue // 末尾换行产生的空串
			}
			return nil, fmt.Errorf("diff 行与 hunk 头的行数不符: %q", line)
		}
		if remOld == 0 && remNew == 0 {
			flush()
		}
	}
	if inHunk {
		return nil, errors.New("diff 被截断：hunk 行数不足")
	}
	if len(out) == 0 && strings.TrimSpace(diff) != "" {
		return nil, errors.New("diff 中没有可解析的 hunk")
	}
	return out, nil
}
func parseHunkHeader(h string) (oldStart, oldCount, newCount int, err error) {
	f := strings.Fields(h)
	if len(f) < 3 || !strings.HasPrefix(f[1], "-") || !strings.HasPrefix(f[2], "+") {
		return 0, 0, 0, fmt.Errorf("无法解析 hunk 头: %s", h)
	}
	parse := func(spec string) (int, int, bool) {
		start, count := spec, "1"
		if i := strings.IndexByte(spec, ','); i >= 0 {
			start, count = spec[:i], spec[i+1:]
		}
		a, err1 := strconv.Atoi(start)
		b, err2 := strconv.Atoi(count)
		return a, b, err1 == nil && err2 == nil
	}
	a, b, ok1 := parse(f[1][1:])
	_, d, ok2 := parse(f[2][1:])
	if !ok1 || !ok2 {
		return 0, 0, 0, fmt.Errorf("无法解析 hunk 头: %s", h)
	}
	return a, b, d, nil
}
func isSystemIgnoredFile(path string) bool {
	clean := filepath.ToSlash(filepath.Clean(strings.TrimSpace(path)))
	clean = strings.TrimPrefix(clean, "./")
	clean = strings.TrimPrefix(clean, "/")
	lower := strings.ToLower(clean)
	base := filepath.Base(lower)
	if base == "agents.md" || base == "readme.md" || base == "instructions.md" {
		return true
	}
	if strings.HasPrefix(lower, ".git/") || lower == ".git" ||
		strings.HasPrefix(lower, ".codex/") || lower == ".codex" ||
		strings.HasPrefix(lower, "codex_workspace/") || lower == "codex_workspace" {
		return true
	}
	return false
}
func IsFauxSandboxCompletion(text string) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return false
	}
	lower := strings.ToLower(trimmed)
	signatures := []string{
		"已创建", "已生成", "已保存", "已写入",
		"创建了文件", "生成了文件", "保存至", "输出到文件",
		":1`", ":1\n", ":1.", ":1 ", // 上游沙箱文件行号引用标记 (如 `pelican.html:1`)
		"created `", "created file", "written to",
		"saved to", "successfully created",
	}
	for _, sig := range signatures {
		if strings.Contains(lower, sig) {
			return true
		}
	}
	return false
}
func SynthesizeDeltaFilesExecJS(files []prism.CodexDeltaFile, isWindows bool) string {
	if len(files) == 0 {
		return ""
	}
	var sb strings.Builder
	idx := 0
	emit := func(cmd string) {
		fmt.Fprintf(&sb, "const out%d = await tools.exec_command({ cmd: ", idx)
		writeJSONString(&sb, cmd)
		fmt.Fprintf(&sb, " });\ntext(out%d);\n", idx)
		idx++
	}
	note := func(msg string) {
		sb.WriteString("text(")
		writeJSONString(&sb, msg)
		sb.WriteString(");\n")
	}
	emitted := 0
	for _, f := range files {
		plan, err := planDeltaFile(f)
		if errors.Is(err, errIgnoredFile) {
			continue
		}
		if err != nil {
			note("[oaiprism] 跳过上游文件变更 " + f.FilePath + "：" + err.Error())
			continue
		}
		var cmds []string
		switch plan.Kind {
		case editDelete:
			cmds = []string{deleteFileCmd(plan.Path, isWindows)}
		case editWrite:
			cmds = writeFileCmds(plan.Path, plan.Content, isWindows)
		case editPatch:
			c := patchFileCmd(plan.Path, plan.Hunks, isWindows)
			if isWindows && len(c) > psCmdSplitThreshold {
				note("[oaiprism] 跳过上游文件变更 " + plan.Path + "：补丁过大，超出 Windows 命令行长度上限，请让模型分步修改")
				continue
			}
			cmds = []string{c}
		}
		for _, c := range cmds {
			emit(c)
		}
		emitted++
	}
	if emitted == 0 && sb.Len() == 0 {
		return ""
	}
	return strings.TrimSpace(sb.String())
}
func psQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

func shQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func psFullPath(rel string) string {
	return "$p=[IO.Path]::Combine((Get-Location).ProviderPath," + psQuote(filepath.FromSlash(rel)) + ")"
}
func deleteFileCmd(rel string, isWindows bool) string {
	if isWindows {
		return psFullPath(rel) + "; if ([IO.File]::Exists($p)) { [IO.File]::Delete($p); 'deleted " + strings.ReplaceAll(rel, "'", "''") + "' }"
	}
	return "rm -f -- " + shQuote(rel)
}
func writeFileCmds(rel, content string, isWindows bool) []string {
	data := []byte(content)
	if !isWindows {
		return []string{"mkdir -p -- \"$(dirname -- " + shQuote(rel) + ")\" && printf '%s' " +
			shQuote(base64.StdEncoding.EncodeToString(data)) + " | base64 --decode > " + shQuote(rel) + " && echo " + shQuote("wrote "+rel)}
	}
	const chunk = 15000 // 原始字节；Base64 后约 20,000 字符
	var cmds []string
	for off := 0; off == 0 || off < len(data); off += chunk {
		end := off + chunk
		if end > len(data) {
			end = len(data)
		}
		b64 := base64.StdEncoding.EncodeToString(data[off:end])
		if off == 0 {
			cmds = append(cmds, psFullPath(rel)+"; $d=[IO.Path]::GetDirectoryName($p); if (-not [IO.Directory]::Exists($d)) { [void][IO.Directory]::CreateDirectory($d) }; "+
				"[IO.File]::WriteAllBytes($p,[Convert]::FromBase64String('"+b64+"')); 'wrote "+strings.ReplaceAll(rel, "'", "''")+"'")
		} else {
			cmds = append(cmds, psFullPath(rel)+"; $x=[Convert]::FromBase64String('"+b64+"'); $f=[IO.File]::Open($p,'Append'); try { $f.Write($x,0,$x.Length) } finally { $f.Close() }")
		}
		if end >= len(data) {
			break
		}
	}
	return cmds
}
func patchFileCmd(rel string, hunks []editHunk, isWindows bool) string {
	b64 := func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }
	if isWindows {
		var sb strings.Builder
		sb.WriteString("$ErrorActionPreference='Stop'; " + psFullPath(rel) + "; ")
		sb.WriteString("$b=[IO.File]::ReadAllBytes($p); $bom=($b.Length -ge 3 -and $b[0] -eq 0xEF -and $b[1] -eq 0xBB -and $b[2] -eq 0xBF); ")
		sb.WriteString("$t=(New-Object Text.UTF8Encoding($false)).GetString($b); if ($bom) { $t=$t.Substring(1) }; ")
		sb.WriteString("$crlf=$t.Contains(\"`r`n\"); $t=$t.Replace(\"`r`n\",\"`n\"); ")
		sb.WriteString("function D([string]$s) { [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String($s)).Replace(\"`r`n\",\"`n\") }; ")
		sb.WriteString("$H=@(); ")
		for _, h := range hunks {
			fmt.Fprintf(&sb, "$H+=,@('%s','%s',%d); ", b64(h.Old), b64(h.New), h.Line)
		}
		sb.WriteString("$shift=0; foreach ($h in $H) { $o=D $h[0]; $n=D $h[1]; $want=[int]$h[2]+$shift; ")
		sb.WriteString("if ($o.Length -eq 0) { $pos=0; for ($k=0; $k -lt $want; $k++) { $nx=$t.IndexOf(\"`n\",$pos); if ($nx -lt 0) { $pos=$t.Length; break }; $pos=$nx+1 }; $t=$t.Insert($pos,$n) } ")
		sb.WriteString("else { $best=-1; $bd=[int]::MaxValue; $i=$t.IndexOf($o,[StringComparison]::Ordinal); ")
		sb.WriteString("while ($i -ge 0) { $ln=$t.Substring(0,$i).Split(\"`n\").Count; $dd=[Math]::Abs($ln-$want); if ($dd -lt $bd) { $bd=$dd; $best=$i }; $i=$t.IndexOf($o,$i+1,[StringComparison]::Ordinal) }; ")
		sb.WriteString("if ($best -lt 0) { throw ('patch context not found, file left unchanged: ' + $p) }; $t=$t.Substring(0,$best)+$n+$t.Substring($best+$o.Length) }; ")
		sb.WriteString("$shift+=($n.Split(\"`n\").Count-1)-($o.Split(\"`n\").Count-1) }; ")
		sb.WriteString("if ($crlf) { $t=$t.Replace(\"`n\",\"`r`n\") }; [IO.File]::WriteAllText($p,$t,(New-Object Text.UTF8Encoding($bom))); 'patched " + strings.ReplaceAll(rel, "'", "''") + "'")
		return sb.String()
	}

	var hs strings.Builder
	for _, h := range hunks {
		fmt.Fprintf(&hs, "('%s','%s',%d),", b64(h.Old), b64(h.New), h.Line)
	}
	return "python3 - " + shQuote(rel) + " <<'OAIPRISM_PATCH'\n" +
		"import sys,base64\n" +
		"p=sys.argv[1]\n" +
		"H=[" + hs.String() + "]\n" +
		"raw=open(p,'rb').read()\n" +
		"bom=raw.startswith(b'\\xef\\xbb\\xbf')\n" +
		"t=(raw[3:] if bom else raw).decode('utf-8')\n" +
		"crlf='\\r\\n' in t\n" +
		"t=t.replace('\\r\\n','\\n')\n" +
		"D=lambda s: base64.b64decode(s).decode('utf-8').replace('\\r\\n','\\n')\n" +
		"shift=0\n" +
		"for o,n,line in H:\n" +
		"    o=D(o); n=D(n); want=line+shift\n" +
		"    if not o:\n" +
		"        pos=0\n" +
		"        for _ in range(want):\n" +
		"            nx=t.find('\\n',pos)\n" +
		"            if nx<0:\n" +
		"                pos=len(t); break\n" +
		"            pos=nx+1\n" +
		"        t=t[:pos]+n+t[pos:]\n" +
		"    else:\n" +
		"        best=-1; bd=None; i=t.find(o)\n" +
		"        while i>=0:\n" +
		"            d=abs(t.count('\\n',0,i)+1-want)\n" +
		"            if bd is None or d<bd: bd=d; best=i\n" +
		"            i=t.find(o,i+1)\n" +
		"        if best<0: sys.exit('patch context not found, file left unchanged: '+p)\n" +
		"        t=t[:best]+n+t[best+len(o):]\n" +
		"    shift+=n.count('\\n')-o.count('\\n')\n" +
		"if crlf: t=t.replace('\\n','\\r\\n')\n" +
		"open(p,'wb').write((b'\\xef\\xbb\\xbf' if bom else b'')+t.encode('utf-8'))\n" +
		"print('patched',p)\n" +
		"OAIPRISM_PATCH"
}
