package main

// Windows version info and manifest, embedded into tsnmp.exe from the
// committed resource_windows_*.syso files (ignored on other platforms). An
// exe without them is more likely flagged by antivirus heuristics.
// Regenerate after editing versioninfo.json or tsnmp.exe.manifest:
//
//go:generate go run github.com/josephspurrier/goversioninfo/cmd/goversioninfo@v1.4.1 -platform-specific
