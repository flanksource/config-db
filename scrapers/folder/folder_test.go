package folder

import (
	"testing"
	"time"

	v1 "github.com/flanksource/config-db/api/v1"
)

func TestNewFolderFilterContext(t *testing.T) {
	tests := []struct {
		name      string
		filter    v1.FolderFilter
		allowDir  bool
		wantErr   bool
		checkFunc func(*v1.FolderFilterContext) bool
	}{
		{
			name: "valid minAge duration",
			filter: v1.FolderFilter{
				MinAge: "1h",
			},
			allowDir: false,
			wantErr:  false,
			checkFunc: func(ctx *v1.FolderFilterContext) bool {
				return ctx.MinAge != nil && *ctx.MinAge == time.Hour
			},
		},
		{
			name: "valid maxAge duration",
			filter: v1.FolderFilter{
				MaxAge: "30m",
			},
			allowDir: false,
			wantErr:  false,
			checkFunc: func(ctx *v1.FolderFilterContext) bool {
				return ctx.MaxAge != nil && *ctx.MaxAge == 30*time.Minute
			},
		},
		{
			name: "invalid minAge duration",
			filter: v1.FolderFilter{
				MinAge: "invalid",
			},
			allowDir: false,
			wantErr:  true,
		},
		{
			name: "valid regex pattern",
			filter: v1.FolderFilter{
				Regex: `^test.*\.txt$`,
			},
			allowDir: false,
			wantErr:  false,
			checkFunc: func(ctx *v1.FolderFilterContext) bool {
				return ctx.RegexComp != nil
			},
		},
		{
			name: "invalid regex pattern",
			filter: v1.FolderFilter{
				Regex: `[`,
			},
			allowDir: false,
			wantErr:  true,
		},
		{
			name: "valid glob pattern",
			filter: v1.FolderFilter{
				Glob: `*.txt`,
			},
			allowDir: false,
			wantErr:  false,
			checkFunc: func(ctx *v1.FolderFilterContext) bool {
				return ctx.GlobComp != nil
			},
		},
		{
			name: "allowDir is set",
			filter: v1.FolderFilter{},
			allowDir: true,
			wantErr:  false,
			checkFunc: func(ctx *v1.FolderFilterContext) bool {
				return ctx.AllowDir == true
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := newFolderFilterContext(tt.filter, tt.allowDir)
			if (err != nil) != tt.wantErr {
				t.Errorf("newFolderFilterContext() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && tt.checkFunc != nil && !tt.checkFunc(got) {
				t.Errorf("newFolderFilterContext() check function failed")
			}
		})
	}
}
