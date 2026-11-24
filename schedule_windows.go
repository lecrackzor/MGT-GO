//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"market-terminal/internal/utils"
)

const autoStartTaskName = "Market Terminal Gexbot"

func syncAutoStartTask(enabled bool) error {
	// This process is the running task. Replacing the task stops that run,
	// which closes the window a moment after it appears.
	if launchedByScheduler() {
		return nil
	}
	if !enabled {
		cmd := hiddenCommand("schtasks", "/Delete", "/TN", autoStartTaskName, "/F")
		out, err := cmd.CombinedOutput()
		if err != nil && !containsTaskMissing(string(out)) {
			return fmt.Errorf("remove start task: %w (%s)", err, out)
		}
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	launch := nextWeekdayLaunch(time.Now())
	xmlPath, err := writeAutoStartTask(exe, launch)
	if err != nil {
		return err
	}
	defer os.Remove(xmlPath)
	cmd := hiddenCommand("schtasks", "/Create", "/F", "/TN", autoStartTaskName, "/XML", xmlPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("create start task: %w (%s)", err, out)
	}
	return nil
}

func nextWeekdayLaunch(now time.Time) time.Time {
	hour, minute := utils.PreOpenLaunchClock(now)
	launch := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, now.Location())
	if !now.Before(launch) {
		launch = launch.Add(24 * time.Hour)
	}
	for launch.Weekday() == time.Saturday || launch.Weekday() == time.Sunday {
		launch = launch.Add(24 * time.Hour)
	}
	return launch
}

func writeAutoStartTask(exe string, launch time.Time) (string, error) {
	body := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-16"?>
<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <RegistrationInfo>
    <URI>\Market Terminal Gexbot</URI>
  </RegistrationInfo>
  <Principals>
    <Principal id="Author">
      <LogonType>InteractiveToken</LogonType>
    </Principal>
  </Principals>
  <Settings>
    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>
    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>
    <StartWhenAvailable>true</StartWhenAvailable>
    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>
    <AllowHardTerminate>true</AllowHardTerminate>
    <ExecutionTimeLimit>PT0S</ExecutionTimeLimit>
    <Enabled>true</Enabled>
  </Settings>
  <Triggers>
    <CalendarTrigger>
      <StartBoundary>%s</StartBoundary>
      <ScheduleByWeek>
        <WeeksInterval>1</WeeksInterval>
        <DaysOfWeek>
          <Monday />
          <Tuesday />
          <Wednesday />
          <Thursday />
          <Friday />
        </DaysOfWeek>
      </ScheduleByWeek>
    </CalendarTrigger>
  </Triggers>
  <Actions Context="Author">
    <Exec>
      <Command>%s</Command>
      <Arguments>--scheduled-start</Arguments>
      <WorkingDirectory>%s</WorkingDirectory>
    </Exec>
  </Actions>
</Task>
`, launch.Format("2006-01-02T15:04:05"), xmlText(exe), xmlText(filepath.Dir(exe)))
	path := filepath.Join(os.TempDir(), "market-terminal-autostart.xml")
	encoded := utf16LEBOM(body)
	if err := os.WriteFile(path, encoded, 0600); err != nil {
		return "", err
	}
	return path, nil
}

func xmlText(value string) string {
	replacer := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
	)
	return replacer.Replace(value)
}

func utf16LEBOM(text string) []byte {
	runes := []rune(text)
	out := make([]byte, 2+len(runes)*2)
	out[0], out[1] = 0xFF, 0xFE
	for i, r := range runes {
		out[2+i*2] = byte(r)
		out[3+i*2] = byte(r >> 8)
	}
	return out
}

func hiddenCommand(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x08000000, // CREATE_NO_WINDOW
	}
	return cmd
}

func launchedByScheduler() bool {
	return hasArg("--scheduled-start") || hasArg("--archive-run")
}

func hasArg(arg string) bool {
	for _, a := range os.Args[1:] {
		if a == arg {
			return true
		}
	}
	return false
}

func containsTaskMissing(text string) bool {
	lower := strings.ToLower(text)
	return strings.Contains(lower, "cannot find") || strings.Contains(lower, "does not exist")
}

const archiveTaskName = "Market Terminal Gexbot Archive"

// syncArchiveTask registers or removes the headless archive task. It starts the
// executable with --archive-run at the configured ET time on weekdays, and the
// process downloads the day's archive for every enabled ticker then exits.
func syncArchiveTask(enabled bool, timeStr string) error {
	if !enabled {
		cmd := hiddenCommand("schtasks", "/Delete", "/TN", archiveTaskName, "/F")
		out, err := cmd.CombinedOutput()
		if err != nil && !containsTaskMissing(string(out)) {
			return fmt.Errorf("remove archive task: %w (%s)", err, out)
		}
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	hourET, minuteET := parseETClock(timeStr)
	launch := nextArchiveLaunch(time.Now(), hourET, minuteET)
	xmlPath, err := writeArchiveTask(exe, launch)
	if err != nil {
		return err
	}
	defer os.Remove(xmlPath)
	cmd := hiddenCommand("schtasks", "/Create", "/F", "/TN", archiveTaskName, "/XML", xmlPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("create archive task: %w (%s)", err, out)
	}
	return nil
}

// nextArchiveLaunch is the next weekday occurrence of the archive clock time in
// local time.
func nextArchiveLaunch(now time.Time, hourET, minuteET int) time.Time {
	hour, minute := utils.ArchiveTaskClock(now, hourET, minuteET)
	launch := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, now.Location())
	if !now.Before(launch) {
		launch = launch.Add(24 * time.Hour)
	}
	for launch.Weekday() == time.Saturday || launch.Weekday() == time.Sunday {
		launch = launch.Add(24 * time.Hour)
	}
	return launch
}

// parseETClock reads "HH:MM" as an Eastern Time clock, falling back to 17:15.
func parseETClock(value string) (int, int) {
	value = strings.TrimSpace(value)
	parts := strings.SplitN(value, ":", 2)
	if len(parts) != 2 {
		return 17, 15
	}
	var hour, minute int
	if _, err := fmt.Sscanf(parts[0]+":"+parts[1], "%d:%d", &hour, &minute); err != nil {
		return 17, 15
	}
	if hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return 17, 15
	}
	return hour, minute
}

func writeArchiveTask(exe string, launch time.Time) (string, error) {
	body := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-16"?>
<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <RegistrationInfo>
    <URI>\Market Terminal Gexbot Archive</URI>
  </RegistrationInfo>
  <Principals>
    <Principal id="Author">
      <LogonType>InteractiveToken</LogonType>
    </Principal>
  </Principals>
  <Settings>
    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>
    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>
    <StartWhenAvailable>true</StartWhenAvailable>
    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>
    <AllowHardTerminate>true</AllowHardTerminate>
    <ExecutionTimeLimit>PT0S</ExecutionTimeLimit>
    <Enabled>true</Enabled>
  </Settings>
  <Triggers>
    <CalendarTrigger>
      <StartBoundary>%s</StartBoundary>
      <ScheduleByWeek>
        <WeeksInterval>1</WeeksInterval>
        <DaysOfWeek>
          <Monday />
          <Tuesday />
          <Wednesday />
          <Thursday />
          <Friday />
        </DaysOfWeek>
      </ScheduleByWeek>
    </CalendarTrigger>
  </Triggers>
  <Actions Context="Author">
    <Exec>
      <Command>%s</Command>
      <Arguments>--archive-run</Arguments>
      <WorkingDirectory>%s</WorkingDirectory>
    </Exec>
  </Actions>
</Task>
`, launch.Format("2006-01-02T15:04:05"), xmlText(exe), xmlText(filepath.Dir(exe)))
	path := filepath.Join(os.TempDir(), "market-terminal-archive-task.xml")
	encoded := utf16LEBOM(body)
	if err := os.WriteFile(path, encoded, 0600); err != nil {
		return "", err
	}
	return path, nil
}
