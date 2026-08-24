package buildinfo

import (
	"runtime/debug"
	"strconv"

	"github.com/yyl1212/agent-studio/sdk/go/agentnode"
	"golang.org/x/mod/module"
)

const developmentVersion = "0.3.1-dev"

var versionOverride string

type Info struct {
	Version    string
	SDKVersion string
	APIVersion string
	Revision   string
	Dirty      bool
}

func Current() Info {
	info, ok := debug.ReadBuildInfo()
	return resolve(versionOverride, info, ok)
}

func resolve(override string, info *debug.BuildInfo, ok bool) Info {
	current := Info{
		Version:    developmentVersion,
		SDKVersion: agentnode.Version,
		APIVersion: agentnode.APIVersion,
		Revision:   "unknown",
	}
	if override != "" {
		current.Version = override
	} else if ok && info != nil && info.Main.Version != "" && info.Main.Version != "(devel)" && !module.IsPseudoVersion(info.Main.Version) {
		current.Version = info.Main.Version
	}
	if !ok || info == nil {
		return current
	}
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			if setting.Value != "" {
				current.Revision = setting.Value
			}
		case "vcs.modified":
			if dirty, err := strconv.ParseBool(setting.Value); err == nil {
				current.Dirty = dirty
			}
		}
	}
	return current
}
