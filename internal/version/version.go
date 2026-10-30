// Package version holds tts-axi's version number and build metadata.
package version

import "runtime/debug"

// Version is bumped by hand with every meaningful feature (see CHANGELOG.md).
// Keep it in sync with the top released section of the changelog.
const Version = "0.2.0"

// Commit returns the short git commit the binary was built from.
//
// Go stamps VCS information into every binary built with `go build` or
// `go install` inside a git checkout; debug.ReadBuildInfo exposes it, so no
// -ldflags are needed. It returns "unknown" when the information is missing
// (for example in `go test` binaries).
func Commit() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	commit, dirty := "", false
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			commit = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if commit == "" {
		return "unknown"
	}
	if len(commit) > 7 {
		commit = commit[:7]
	}
	if dirty {
		commit += "-dirty"
	}
	return commit
}

// String is the one-line form printed by `tts-axi version`.
func String() string {
	return "tts-axi " + Version + " (" + Commit() + ")"
}
