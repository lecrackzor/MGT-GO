# Setup Instructions

## Step 1: Install Go

1. Download Go from: https://go.dev/dl/
2. Run the installer (default location: `C:\Program Files\Go`)
3. Verify installation:
   ```powershell
   go version
   ```
   Should show something like: `go version go1.21.x windows/amd64`

## Step 2: Install Wails CLI

```powershell
go install github.com/wailsapp/wails/v3/cmd/wails3@latest
```

Add Go bin directory to PATH if needed:
```powershell
$env:PATH += ";$env:USERPROFILE\go\bin"
```

Verify installation:
```powershell
wails3 version
```

## Step 3: Initialize Go Module

```powershell
cd GO
go mod init market-terminal
go mod tidy
```

## Step 4: Run the Application

### Development Mode (with hot reload)
```powershell
cd GO
wails3 dev
```

This will:
- Start the application
- Automatically reload on code changes
- Show console output for debugging

### Production Build
```powershell
cd GO
wails3 build
```

This creates `build/bin/market-terminal.exe` that you can run directly.

## Troubleshooting

### "go: command not found"
- Make sure Go is installed and in your PATH
- Restart your terminal after installing Go

### "wails3: command not found"
- Make sure `$env:USERPROFILE\go\bin` is in your PATH
- Or use full path: `$env:USERPROFILE\go\bin\wails3.exe`

### Build errors
- Run `go mod tidy` to download dependencies
- Make sure you're in the `GO/` directory

## Next Steps

Once the basic app runs, we'll start migrating components:
1. Settings and configuration
2. Database operations
3. API client
4. Data collection
5. Chart management
6. Frontend UI
