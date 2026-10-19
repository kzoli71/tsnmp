# AGENTS.md

SNMPv2c / SNMPv3 interface traffic monitor with a TUI (Go, tview/tcell, gosnmp). It must run on both Linux and Windows 10+ / Server 2016+ (the minimum of Go 1.21+). The program's interface (all user-facing text, error messages, help) and `Readme.md` are in English; new text may only be added in English. The only exception is `Readme.hu.md`, the Hungarian translation of `Readme.md`: keep the two in sync when either changes.

## Commands

```sh
GOTOOLCHAIN=local go build -trimpath -o tsnmp .
GOTOOLCHAIN=local go vet ./... && GOTOOLCHAIN=local go test ./...
GOTOOLCHAIN=local GOOS=windows GOARCH=amd64 go build -trimpath -o tsnmp.exe .   # always check this
GOTOOLCHAIN=local GOFLAGS=-mod=mod go generate   # after changing versioninfo.json / tsnmp.exe.manifest / assets/tsnmp.ico
GOTOOLCHAIN=local go run ./tools/licenses -write  # after changing dependencies (without -write: check only)
```

## Repository and CI

Two remotes: `origin` (local Gitea) and `github` (`kzoli71/tsnmp`, private for now, public later). `main` is identical on both: always push to both (`git push origin main && git push github main`). The GitHub repo started with a clean history; the earlier history exists only in the Gitea `archive/pre-github` tag, and this tag must never be pushed to GitHub (no `git push --tags` or `--mirror`). GitHub Actions (`.github/workflows/build.yml`) runs the license check (`go run ./tools/licenses`) + vet + `test -race` on every push to `main`, then builds static Linux and Windows amd64 binaries as artifacts. `.github/workflows/lint.yml` runs `staticcheck` and `gosec -exclude=G115` with Go 1.26 (pinned together with the linter versions; gosec v2.29.0 cannot read Go 1.27 export data); silence a justified gosec finding with `// #nosec Gxxx -- reason`. Releases: pushing a `v*` tag runs `.github/workflows/release.yml` (checks, then GoReleaser with `.goreleaser.yaml`: linux and windows amd64 archives only (no arm64: the user cannot test it), `checksums.txt`, changelog from git) into a **draft** release. The user then runs `sign-release -R kzoli71/tsnmp -a 'tsnmp_{version}_windows_amd64.zip' vX.Y.Z` on the home server (a separate script in the private `kzoli71/sign-release` repo, not part of this repository): it signs the Windows exe with the Certum SimplySign cloud certificate (`ssign`; the one-time code is typed at the prompt), verifies it with `osslsigncode`, re-uploads the zip and `checksums.txt`, and publishes the release. Never read, ask for, print or commit the otpauth URI, the SimplySign password or a one-time code. The version reaches the binary via `-ldflags "-X main.version=…"` (`tsnmp -version`; `dev` otherwise); keep the GoReleaser ldflags free of `-s -w`. Before tagging, bump `versioninfo.json` (the Windows file version) and run `go generate`. Tags may be pushed to GitHub only when the user asks. License: MIT (`LICENSE`).

## Git and pull requests

- Commit messages and PR titles follow [Conventional Commits](https://www.conventionalcommits.org/), in English and lowercase: `feat: …`, `fix: …`, `docs: …`, `chore: …`.
- Branch names: `feat/<short-name>`, `fix/<short-name>` or `docs/<short-name>`.
- Commits are signed with an SSH key (`gpg.format ssh`, `commit.gpgsign true`).
- No tool or AI attribution anywhere: no `Co-Authored-By` trailers, session links or "generated with" lines in commits, PR descriptions or comments.
- PR descriptions are written in plain English for a human reviewer.

## Dependencies

The machine has Go 1.26. `go.mod` requires Go 1.24, the minimum of the current tcell (v2.13) and tview (v0.42); the build workflow checks with exactly this version, the release is built with the latest stable Go. Keep golang.org/x/* at the versions tcell requires: their latest releases need a newer Go and would raise the minimum. Run `go get` / `go mod tidy` with `GOTOOLCHAIN=local`, and keep `go.mod` free of a `toolchain` line.

## Layout

- `main.go`: `App` struct, command-line flags (`loginFromFlags`, net-snmp style: `-v -c -u -a -A -x -X -n`), pages (`login` / `main` / `graph`), global keys (`globalKeys`), poll interval setting.
- `login.go`: login screen (`loginState`, `loginView`): changing the version dropdown rebuilds the form (v2c: community; v3: user, auth/priv protocol and password, context name), `parseTarget`, `connect`.
- `snmp.go`: `Config` (v2c/v3 settings, `Validate`, `Describe`/`Short` for the header; the v3 security level follows from the protocols), `Client`. v3 Report errors (`gosnmp.ErrWrongDigest` etc.) are translated into readable text by `snmpErr`. Not thread-safe: only the poller goroutine uses it (except `SysName` during login). Every query goes through `walkTable`: multiple columns in one GetBulk, with `sysUpTime` as a non-repeater; a row's timestamp (`stamp`) is the device uptime from the response that delivered the row's first column.
- `poller.go`: periodic discovery (60 s: names/description/speed; immediately on an unknown ifIndex) and, in every poll, querying the status and counters of all interfaces (`Client.Poll`). One cycle is `cycle()` (callable directly from tests). If a discovery has already succeeded, a discovery error does not stop measuring: it retries after `discoveryRetry` (30 s), and the error goes to the UI in `PollResult.DiscErr`. Results are handed to the UI via `app.QueueUpdateDraw`, because the UI may only be modified from the tview goroutine.
- `history.go`: compact sample storage (`packedSample`, 20 bytes, pointer-free, in blocks of 512). A copy of a `History` value is a snapshot readable without locking, because the writer only writes beyond the copy's length.
- `store.go`: mutex-protected in-memory store. Every interface is measured (admin down too); filtering (`a`/`o` keys) happens only in the UI (`MainView.visible`). Samples are cleared only when measurement is restarted (`r`, `Store.Reset`); `Snapshot`/`Get` return a `History` snapshot.
- `licenses.go`: embeds `LICENSE` and `THIRD_PARTY_LICENSES.txt` (`licensesText`), for the `-licenses` flag and the Licenses page (`l` on the help page; plain text, no tview color codes).
- `tools/licenses`: generates `THIRD_PARTY_LICENSES.txt` (stdlib only): the modules of `go list -deps .` for linux + windows amd64, their LICENSE/LICENCE/COPYING (+ NOTICE, PATENTS) files, SPDX detected from the text; only MIT, BSD-2/3-Clause, ISC, Apache-2.0, MPL-2.0 are accepted, anything else or a missing file is an error. The Go license comes from `GOROOT/LICENSE`, or a built-in copy if the Go installation has none (Ubuntu's). Texts are normalized (LF, no trailing spaces), so the check works on Windows too. `TestThirdPartyLicenses` checks that every `go.mod` require is listed.
- `ui_main.go`: table (filters, `sortModes`, search); `ui_graph.go`: custom braille line chart; `format.go`: formatting helpers (including `naturalLess`).
- `winres.go`, `versioninfo.json`, `tsnmp.exe.manifest`, `resource_windows_*.syso`: Windows version info, manifest and icon (`IconPath`: `assets/tsnmp.ico`) (against Defender's ML-based false positive, `Wacatac.B!ml`). The `.syso` files are committed; `goversioninfo` runs only via `go run` and is not in `go.mod`. Do not use `-ldflags="-s -w"`.
- `assets/`: logo (`logo.svg`, text converted to paths) and icon (`icon.svg`; `icon-small.svg` is a simplified drawing for 16–32 px; `tsnmp.ico` holds 16–256 px). Not MIT licensed (see `Readme.md`).
- `internal/usm`: v3 protocol names (`AuthNames`/`PrivNames`, in the order of the gosnmp constants) and their parsing; used by both tsnmp and the fakeagent.
- `internal/fakeagent`: test agent package (v2c, and with `Options.V3` SNMPv3 at the same time; `v3.go`: its own USM layer, decoding/authenticating/encrypting with gosnmp) (`snmp_test.go` also starts it on a random port); `tools/fakeagent` is just its command-line wrapper.

## Non-obvious decisions

- Packet counters are stored per part (`[3]uint64`: ucast/mcast/bcast or ucast/nucast), and the delta is computed per part (`partsDelta`), because 32-bit counters wrap independently of each other.
- HC vs. 32-bit mode: every poll walks the HC columns; the 32-bit `ifTable` columns are walked only if the HC value is missing for some interface.
- `computeRate` computes from the `sysUpTime` difference (not local time), and drops the sample if uptime went backwards, `ifCounterDiscontinuityTime` changed, or a 32-bit wrap would give a rate above 1.2× `ifHighSpeed`/`ifSpeed`. The speed limit is intentionally applied only on wrap: tunnel/virtual interfaces report unrealistically low speeds.
- Numbers that don't fit into a 32-bit `int` (`ifSpeed`, `TimeTicks`) need `pduUint64`, not `pduInt` (it would overflow on 32-bit ARM).
- 32-bit fallback: for at most `maxTargetedIfaces` interfaces, targeted GET (`getRows`, with `sysUpTime.0` in every request), otherwise a walk of the 32-bit table.
- Confirmation dialogs (quit, restart): `App.confirm`; on the `confirm` page `globalKeys` handles the `y`/`n` keys. Right-click on the graph only goes back if `graph` is the top page (the `Modal` passes clicks outside of it through).
- In this tview version `QueueUpdateDraw` waits for the update to run: from an event handler (e.g. a dropdown callback) it may only be called as `go a.app.QueueUpdateDraw(...)`, otherwise it deadlocks (this is how version switching froze).
- On the login page, with an open dropdown list (focus on a `*tview.List`), Esc belongs to the list and does not quit.
- The fakeagent's SNMPv3 behaves like a real agent: engine ID discovery (`usmStatsUnknownEngineIDs`), unknown user, mismatched security level, wrong digest, decryption error, and unknown context (`snmpUnknownContexts`) with a Report PDU; gosnmp returns the latter as `ErrUnknownReportPDU`, and `snmpErr` points to the context name. After `Reboot`, `snmpEngineBoots` increases and it sends a `usmStatsNotInTimeWindows` report (gosnmp resyncs from it, `TestV3AgentReboot`). The v3 header fields are read by a custom BER reader (`parseV3Header`), because they are needed for error reporting before authentication.
- Stale data (`App.stale`): the last poll failed, or there has been no successful poll for a long time. A 1 s ticker also checks this, because with a broken connection the poll sits on timeouts for a long time. Indication: red text in the table title and on the graph's bottom border, grey cells.
- Rate above speed (`findOverSpeed`, `App.overSpeed`, on the graph `GraphView.overSpeed`, `recentOverSpeed`): in both places only the samples from the last `overSpeedShow` (1 minute) count, not the older ones visible on the graph; it is only a warning, the sample is neither dropped nor clipped. The cause is routers' counter caching (e.g. on a Nokia 7750 SR with a 3 s poll, 10.7 Gb/s on a 10G port): with short poll intervals samples alternate between too high and too low, the average is correct. The fakeagent reproduces this with `-refresh 2s -hot`.
- The table title (`tableTitle`) and description width adapt to the screen width (`App.screenW`, updated by `SetBeforeDrawFunc`, which redraws on resize); tview would truncate a too long title on both sides.
- `pduInt` must also handle the `TimeTicks` type (uptime, discontinuity time) – missing this was why counter reset detection didn't work earlier.
- `renderTable` restores scrolling with `SetOffset` after every rebuild: on a short table (e.g. empty at startup or when filtering) tview turns on "follow the end" mode and would jump to the end when the list grows.
- Rate cells are fixed width (`formatRateFixed`, `rateWidth`), otherwise column widths change from poll to poll ("jumping").
- `renderTable` only calls `table.Select` if the position of the selected row changed, because `Select` scrolls back to the selection (this was the "list jumps to the top" bug).
- Graph scrolling/zoom (`ui_graph.go`): `offset` is the number of points counted backwards from the newest point (0 = live). When scrolled back, `refresh` increases it by the number of new points, so the view stays put. When zoomed, points are averages of `zoom` measurements, and the buckets are aligned to the start of the measurement history (`aggregate`) so a point's value doesn't change with new measurements; min/max are computed from the raw measurements. Each point is exactly one character column (`dotsPerSample` = 2 braille dots), right-aligned; the x axis labels (`timeTicks`) rely on this mapping and are intentionally not time-proportional.
- Everything drawn directly on the graph uses `graphStyle()` (background `tview.Styles.PrimitiveBackgroundColor`), not `tcell.StyleDefault`: the latter is the terminal's own background (aubergine on Ubuntu's GNOME Terminal), visible behind every character. With `TERM=linux` (virtual console, no braille glyphs in its fonts) `cellRune` draws █ instead of braille (the console-setup fonts have no ▀ ▄ either, they show `#`).
- On the `+`/`-` buttons the `MouseLeftDoubleClick` event is treated as a click, otherwise fast clicks get lost.
- User/SNMP text in table cells and titles must be written with `tview.Escape` (`[` counts as a tview color code).
- Release signing (`sign-release`, outside this repo) runs by hand on the home server, not in GitHub Actions. SimplySign Desktop on Linux is a GUI app that needs an interactive login, so the script uses `ssign`, which talks to the SimplySign cloud over HTTPS; it gets only the one-time code, never the TOTP seed. A `workflow_dispatch` taking the 6-digit code was rejected: the code lives ~30 s while a runner takes 10–30 s to start, and workflow inputs are visible to everyone who can read the repo.

## Manual testing

There is no snmpd on the machine; use the fakeagent instead (interfaces: `fakeagent.DefaultIfaces`):

```sh
GOTOOLCHAIN=local go run ./tools/fakeagent [-listen 127.0.0.1:16161] [-community public] [-extra 50] [-delay 30ms] [-refresh 2s] [-hot] [-user tsnmp -auth SHA -authpass authpass123 -priv AES -privpass privpass123 -context ""]
./tsnmp 127.0.0.1:16161
./tsnmp -v 3 -u tsnmp -A authpass123 -X privpass123 127.0.0.1:16161
```

The fakeagent's stdin accepts `reset N` / `reboot` / `down` / `up` / `nodisc` / `disc` commands to simulate a counter reset, reboot, connection loss and discovery failure (when running in the background, via a FIFO: `mkfifo fa.in; fa < fa.in & exec 7>fa.in; echo down >&7`). If tsnmp starts using a new OID, it must also be added to `buildMIB` in `internal/fakeagent` and tested in `snmp_test.go`. `go test -race ./...` must also be run (it needs CGO).

The TUI can be driven in tmux: `tmux send-keys`; for the mouse use SGR sequences, e.g. `$'\e[<0;X;YM'` + `$'\e[<0;X;Ym'` for a click, `$'\e[<65;X;YM'` for scrolling down; check with `tmux capture-pane -p`.
