# Market Terminal Gexbot - Go/Wails Version

> **⚠️ IMPORTANT**: Please read the [DISCLAIMER.md](DISCLAIMER.md) before using this software. This software is not affiliated with Gexbot, is provided "as is" without warranty, and is not financial advice.

## Prerequisites

### Install Go
1. Download Go from https://go.dev/dl/
2. Install Go (default location: `C:\Program Files\Go`)
3. Verify installation:
   ```bash
   go version
   ```
   Should show: `go version go1.21.x windows/amd64` or similar

### Install Wails CLI
```bash
go install github.com/wailsapp/wails/v3/cmd/wails3@latest
```

Verify installation:
```bash
wails3 version
```

## Running the Application

### Development Mode (with hot reload)
```bash
cd GO
wails3 dev
```
This is similar to running `python Market_Terminal_Gexbot.py` - it starts the app and automatically reloads on changes.

### Production Build
```bash
cd GO
wails3 build
```
This creates a single executable in `build/bin/market-terminal.exe` that you can double-click to run.

### Running the Built App
- **Windows**: Double-click `build/bin/market-terminal.exe`
- The executable is standalone - no Python or other dependencies needed

## Project Structure

- `main.go` - Application entry point
- `app.go` - Main application struct
- `internal/` - Go packages (backend logic)
- `frontend/` - Web frontend (HTML/JS/CSS)
- `build/` - Build output (generated)

## Memory Profiling

The app includes built-in memory profiling. While running, access:
- `http://localhost:6060/debug/pprof/heap` - Heap profile
- Use `go tool pprof http://localhost:6060/debug/pprof/heap` to analyze

This gives you complete visibility into all memory allocations - no hidden C-level memory!
