# Developing tsnmp

Notes for contributors and the maintainer. For using the program, see [README.md](README.md).

## Building and testing

Requires Go 1.24+.

```sh
go build -trimpath -o tsnmp .                                  # Linux
GOOS=windows GOARCH=amd64 go build -trimpath -o tsnmp.exe .    # Windows (cross-compiled)
go vet ./...
go test ./...
```

Do not use `-ldflags="-s -w"`: a stripped exe looks more suspicious to antivirus software.

### Testing without a real device

```sh
go run ./tools/fakeagent            # fake SNMP agent: 127.0.0.1:16161, community "public"
./tsnmp 127.0.0.1:16161             # or without an address, on the login screen
./tsnmp -v 3 -u tsnmp -A authpass123 -X privpass123 127.0.0.1:16161   # SNMPv3 (SHA/AES)
```

Flags: `-listen`, `-community`, `-extra` (number of additional interfaces, default 50),
`-delay` (wait before every response, e.g. `50ms`, to simulate a slow device),
`-refresh` (counters change only at this interval, e.g. `2s`, like a router's
counter cache), `-hot` (a 10 Gb/s port with a constant 85% load), and for SNMPv3
`-user` (default `tsnmp`; `""` turns SNMPv3 off), `-auth` / `-authpass` (default
`SHA` / `authpass123`), `-priv` / `-privpass` (default `AES` / `privpass123`;
`-auth none` or `-priv none` lowers the security level), `-context` (the only
accepted context name, default: the default context). The agent answers v2c
with the community and v3 with the user at the same time, and sends the SNMPv3
error reports (unknown user, wrong password, wrong security level, unknown context)
like a real device.
Commands on standard input while running: `reset N` (zero the counters of
ifIndex N), `reboot` (agent restart), `down` / `up` (connection lost /
restored), `nodisc` / `disc` (stop / resume answering discovery requests).

## Continuous integration

GitHub Actions (`.github/workflows/build.yml`) runs the license check and the tests and
builds the Linux and Windows amd64 binaries on every push to `main`; they can be
downloaded from the run's "Artifacts" section.
A second workflow (`.github/workflows/lint.yml`) runs `staticcheck` and `gosec` with a
newer Go (1.26) than the one the program is built with.

## Windows resources

The Windows exe gets version info, a manifest and the program icon embedded
(`versioninfo.json`, `tsnmp.exe.manifest`, `assets/tsnmp.ico` →
`resource_windows_*.syso`). Without version info and a manifest Defender is more
likely to report a false positive (e.g. `Trojan:Win32/Wacatac.B!ml`). Regenerate
after changing any of these files: `go generate`.

## Third-party licenses

[THIRD_PARTY_LICENSES.txt](THIRD_PARTY_LICENSES.txt) is generated:
`go run ./tools/licenses -write` regenerates it (run it after every dependency change),
`go run ./tools/licenses` checks that it is up to date (CI runs the check).

## Releasing

`tools/release.sh` does all of the steps below, asking before each one (`-y`: without
asking), and can be run again after a failure (finished steps are skipped). Run it on a
clean `main` that matches `origin/main` (GitHub); it needs `sign-release` on `PATH` (or its path in
`SIGN_RELEASE`):

```sh
CERTUM_EMAIL=you@example.com tools/release.sh 1.0.2
```

It bumps `versioninfo.json`, runs `go generate` and the checks, merges the bump through
a `release/vX.Y.Z` PR after green CI, tags the merged commit
(signed tag, pushed to GitHub), waits for the draft release and runs
`sign-release`. The SimplySign code is read only by `sign-release`.

By hand:

1. Update the version in `versioninfo.json` and run `go generate`; commit.
2. Push a `v*` tag (e.g. `v1.0.1`). This runs `.github/workflows/release.yml`: after the
   checks, [GoReleaser](https://goreleaser.com/) (`.goreleaser.yaml`) builds the release
   archives, the checksums and the changelog, and uploads them to a draft GitHub release.
3. Sign the Windows exe and publish the release (see below).

### Code signing

The Windows exe is Authenticode-signed with a Certum SimplySign (cloud) code signing
certificate before the release is published, by hand on the maintainer's machine with
`sign-release`, a separate script kept outside this repository
(`kzoli71/sign-release`). It downloads the Windows zip from the draft, signs
`tsnmp.exe` with [ssign](https://github.com/Le-Syl21/ssign) (with a timestamp from
`time.certum.pl`), verifies the signature with `osslsigncode`, uploads the re-packed
zip and the updated `checksums.txt`, and publishes the release after confirmation. It
asks for the current 6-digit code of the SimplySign mobile app (hidden input):

```sh
CERTUM_EMAIL=you@example.com sign-release -R kzoli71/tsnmp \
    -a 'tsnmp_{version}_windows_amd64.zip' v1.0.1
```
