# Quick start

Install Go 1.25 from https://go.dev/dl/ and the Wails v3 CLI:

```powershell
go install github.com/wailsapp/wails/v3/cmd/wails3@latest
```

From this repository:

```powershell
wails3 dev
```

A production build is `wails3 build`, which writes `build/bin/market-terminal.exe`.

If `wails3` is not found, add `%USERPROFILE%\go\bin` to PATH. If modules fail to download, run `go mod tidy` in this repository. Do not run `go mod init`; the module is already `market-terminal`.
