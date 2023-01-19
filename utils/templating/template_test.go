package templating

import (
	"testing"

	v1 "github.com/flanksource/config-db/api/v1"
)

func TestTemplate(t *testing.T) {
	type args struct {
		environment map[string]interface{}
		template    v1.Template
	}
	tests := []struct {
		name    string
		args    args
		want    string
		wantErr bool
	}{
		{
			name: "simple addition",
			args: args{
				environment: map[string]interface{}{
					"age": 27,
				},
				template: v1.Template{Expression: `age + 1`},
			},
			want:    "28",
			wantErr: false,
		},
		{
			name: "simple concat",
			args: args{
				environment: map[string]interface{}{
					"name": "flanksource",
				},
				template: v1.Template{Expression: `"Hello " + name`},
			},
			want:    "Hello flanksource",
			wantErr: false,
		},
		{
			name: "returns bool",
			args: args{
				environment: map[string]interface{}{
					"url":  "flanksource.com",
					"name": "flanksource",
				},
				template: v1.Template{Expression: `url.startsWith(name)`},
			},
			want:    "true",
			wantErr: false,
		},
		{
			name: "expression with no corresponding environment",
			args: args{
				environment: map[string]interface{}{},
				template:    v1.Template{Expression: `name + 2`},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Template(tt.args.environment, tt.args.template)
			if (err != nil) != tt.wantErr {
				t.Errorf("Template() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("Template() = %v, want %v", got, tt.want)
			}
		})
	}
}
