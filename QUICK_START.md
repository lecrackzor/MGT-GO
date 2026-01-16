# Quick Start Guide

## Installation

### 1. Install Go
Download and install from: https://go.dev/dl/

Verify:
```powershell
go version
```

### 2. Install Wails CLI
```powershell
go install github.com/wailsapp/wails/v3/cmd/wails3@latest
```

Add to PATH if needed:
```powershell
$env:PATH += ";$env:USERPROFILE\go\bin"
```

Verify:
```powershell
wails3 version
```

### 3. Initialize Go Module
```powershell
cd GO
go mod init market-terminal
go mod tidy
```

## Running the Application

### Development Mode (with hot reload)
```powershell
cd GO
wails3 dev
```

This is like running `python Market_Terminal_Gexbot.py` - it starts the app and automatically reloads on code changes.

### Production Build
```powershell
cd GO
wails3 build
```

This creates `build/bin/market-terminal.exe` that you can double-click to run (no Python needed!).

## What's Different from Python?

### Running
- **Python**: `python Market_Terminal_Gexbot.py`
- **Go**: `wails3 dev` (development) or double-click `build/bin/market-terminal.exe` (production)

### Memory Profiling
- **Python**: Limited visibility into C-level memory
- **Go**: Full visibility with `go tool pprof` - see every allocation!

### Dependencies
- **Python**: `pip install -r requirements.txt`
- **Go**: `go mod tidy` (automatically downloads dependencies)

## Current Status

✅ Basic app structure
✅ Settings management
⏳ Database layer (next)
⏳ API client (next)
⏳ Data collection (next)
⏳ Frontend UI (next)

## Troubleshooting

### "go: command not found"
- Make sure Go is installed and in PATH
- Restart terminal after installing Go

### "wails3: command not found"
- Make sure `$env:USERPROFILE\go\bin` is in PATH
- Or use full path: `$env:USERPROFILE\go\bin\wails3.exe`

### Build errors
- Run `go mod tidy` to download dependencies
- Make sure you're in the `GO/` directory
