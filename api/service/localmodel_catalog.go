package service

import (
	"github.com/kocort/kocort/api/types"
	"github.com/kocort/kocort/internal/localmodel"
)

func cloneLocalizedText(src *localmodel.LocalizedText) *types.LocalizedText {
	if src == nil {
		return nil
	}
	return &types.LocalizedText{
		Zh: src.Zh,
		En: src.En,
	}
}