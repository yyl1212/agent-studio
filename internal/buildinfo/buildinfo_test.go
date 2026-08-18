package buildinfo

import (
	"reflect"
	"runtime/debug"
	"testing"
)

func TestResolveBuildInfo(t *testing.T) {
	tests := []struct {
		name     string
		override string
		info     *debug.BuildInfo
		ok       bool
		want     Info
	}{
		{
			name:     "override wins over module version",
			override: "v0.2.0-rc.9",
			info: &debug.BuildInfo{
				Main: debug.Module{Version: "v0.2.0-rc.1"},
				Settings: []debug.BuildSetting{
					{Key: "vcs.revision", Value: "abc123"},
					{Key: "vcs.modified", Value: "true"},
				},
			},
			ok: true,
			want: Info{
				Version: "v0.2.0-rc.9", SDKVersion: "0.2.0",
				APIVersion: "agent-studio.dev/v1alpha1",
				Revision:   "abc123", Dirty: true,
			},
		},
		{
			name: "tagged module version",
			info: &debug.BuildInfo{
				Main: debug.Module{Version: "v0.2.0-rc.1"},
				Settings: []debug.BuildSetting{
					{Key: "vcs.revision", Value: "deadbeef"},
					{Key: "vcs.modified", Value: "false"},
				},
			},
			ok: true,
			want: Info{
				Version: "v0.2.0-rc.1", SDKVersion: "0.2.0",
				APIVersion: "agent-studio.dev/v1alpha1",
				Revision:   "deadbeef", Dirty: false,
			},
		},
		{
			name: "development fallback",
			info: &debug.BuildInfo{
				Main: debug.Module{Version: "(devel)"},
				Settings: []debug.BuildSetting{
					{Key: "vcs.modified", Value: "not-a-bool"},
				},
			},
			ok: true,
			want: Info{
				Version: "0.2.0-dev", SDKVersion: "0.2.0",
				APIVersion: "agent-studio.dev/v1alpha1",
				Revision:   "unknown", Dirty: false,
			},
		},
		{
			name: "missing build info",
			want: Info{
				Version: "0.2.0-dev", SDKVersion: "0.2.0",
				APIVersion: "agent-studio.dev/v1alpha1",
				Revision:   "unknown", Dirty: false,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := resolve(test.override, test.info, test.ok)
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("resolve() = %#v, want %#v", got, test.want)
			}
		})
	}
}
