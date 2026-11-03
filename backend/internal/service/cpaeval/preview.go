package cpaeval

// PelicanPreviewFreeze is the runtime string from upstream web/pelican.js.
// JavaScript escapes the closing slash in its source template literal; Go does not.
const PelicanPreviewFreeze = `<script>addEventListener("load", () => requestAnimationFrame(() => setTimeout(() => {
  const hold = () => document.getAnimations().forEach((animation) => { animation.pause(); animation.currentTime = 1000; });
  hold();
  addEventListener("animationstart", hold);
  document.querySelectorAll("svg").forEach((svg) => svg.pauseAnimations());
  for (let id = setTimeout(() => {}); id > 0; id--) { clearTimeout(id); clearInterval(id); }
  window.requestAnimationFrame = window.setTimeout = window.setInterval = () => 0;
}, 1000)));</script>`
