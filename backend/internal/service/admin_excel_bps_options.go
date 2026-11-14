package service

import (
	"context"
	"errors"
)

func (s *adminServiceImpl) validateExcelBPSGroupOptions(ctx context.Context, a *Account) error {
	if err := ValidateExcelBPSAccountOptions(a); err != nil {
		return err
	}
	if a.Extra[ExcelBPSAutoMoveOn403Key] != true {
		return nil
	}
	id, _ := bpsInteger(a.Extra[ExcelBPS403TargetGroupIDKey])
	if s.groupRepo == nil {
		return errors.New("BPS target group validation unavailable")
	}
	group, err := s.groupRepo.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if group == nil || group.Status != StatusActive || (group.Platform != PlatformOpenAI && group.Platform != PlatformComposite) {
		return errors.New("BPS target group must be an active OpenAI-compatible group")
	}
	return nil
}
