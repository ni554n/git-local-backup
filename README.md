# Git Local Backup

A CLI tool for copying local files from Git projects to a cloud drive or a backup disk for safekeeping.
It copies only the files that have been modified since the last backup, including:

- Committed files that are not yet pushed to the remote repository
- Working and staged files that are not yet committed
- Files that are not yet tracked by `git add`
- Any .gitignored file included via `--force-include` flag

> … basically every unpushed file that can be lost during an incident.

## Why?

Most modern editors now have built-in local history feature, so pushing unfinished changes solely
for backup purposes seems counter-productive to me.
To reduce the risk of accidental data loss in between remote pushing,
this tool can provide a layer of protection.

Plus, there's a lot of important files that cannot be committed to VCS,
such as `.env` containing private keys that should be backed up locally with a tool like this.

## Prerequisites

- Git is installed and added to the `PATH` so that it's accessible from anywhere
- Have some sort of a backup solution in place
- Optionally, subscribe to update notifications by selecting `Watch > Custom > Releases` on GitHub

## Usage

Download the [latest release](https://github.com/ni554n/git-local-backup/releases/latest) and
extract (`tar -xvzf`) the binary to a suitable path.

Here's all the options you can configure:

| Flag | Description |
| --- | --- |
| `--projects-path` | Path to the projects directory (required) |
| `--backup-path` | Path to an empty backup directory (required)<br>Otherwise, existing files may be removed from that directory. |
| `--remote-branch` | Remote name (default: `origin`) |
| `--force-include` | Always include a git ignored file or directory like `.git`.<br>Specify it multiple times to include multiple items. |
| `--dry-run` | Preview changes without modifying the backup directory |

### Test drive the command

Assuming all your Git projects are in `~/Projects` and you want to backup to `~/OneDrive/Backup/Projects`:

```sh
/path/to/git-local-backup --projects-path "~/Projects" --backup-path "~/OneDrive/Backup/Projects" --dry-run
```

If you also want to back up Git internals like stashes, or other gitignored files such as `.env`:

```sh
/path/to/git-local-backup --projects-path "~/Projects" --backup-path "~/OneDrive/Backup/Projects" --force-include ".git" --force-include ".env" --dry-run
```

If you are satisfied with the output, remove the `--dry-run` flag, and
schedule the command to run periodically using the instructions below.

<details>
<summary><h3>Linux (Crontab)</h3></summary>

Run `crontab -e` and add the following line:

```txt
*/15 * * * * /path/to/git-local-backup "~/Projects" --backup-path "~/OneDrive/Backup/Projects"
```

</details>

<details>
<summary><h3>MacOS (Launchd)</h3></summary>

1. Create this plist file in the `~/Library/LaunchAgents/` and configure it your command:

`git-local-backup.plist`

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>Git Local Backup</string>
  <key>ProgramArguments</key>
  <array>
    <string>/path/to/git-local-backup --projects-path "~/Projects" --backup-path "~/OneDrive/Backup/Projects"</string>
  </array>
  <key>StartInterval</key>
  <integer>900</integer> <!-- 900 seconds = 15 minutes -->
  <key>RunAtLoad</key>
  <true/>
</dict>
</plist>
```

2. Load it via `launchctl load ~/Library/LaunchAgents/git-local-backup.plist`
3. Start it via `launchctl start git-local-backup`
4. Check the status via `launchctl list | grep git-local-backup`. A status of zero means a successful run.

</details>

<details>
<summary><h3>Windows (Task Scheduler)</h3></summary>

1. Create the following file and configure it with your command. You can also modify it later during import.

`Git Local Backup.xml`

```xml
<?xml version="1.0" encoding="UTF-16"?>
<Task version="1.4" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <RegistrationInfo>
    <Author>ni554n</Author>
    <Description>https://github.com/ni554n/git-local-backup</Description>
    <URI>\ni554n\Git Local Backup</URI>
  </RegistrationInfo>
  <Triggers>
    <LogonTrigger>
      <Repetition>
        <Interval>PT15M</Interval> <!-- 15 minutes -->
        <StopAtDurationEnd>false</StopAtDurationEnd>
      </Repetition>
      <Enabled>true</Enabled>
    </LogonTrigger>
  </Triggers>
  <Principals>
    <Principal id="Author">
      <LogonType>S4U</LogonType>
      <RunLevel>LeastPrivilege</RunLevel>
    </Principal>
  </Principals>
  <Settings>
    <MultipleInstancesPolicy>StopExisting</MultipleInstancesPolicy>
    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>
    <StopIfGoingOnBatteries>true</StopIfGoingOnBatteries>
    <AllowHardTerminate>true</AllowHardTerminate>
    <StartWhenAvailable>false</StartWhenAvailable>
    <RunOnlyIfNetworkAvailable>false</RunOnlyIfNetworkAvailable>
    <IdleSettings>
      <StopOnIdleEnd>true</StopOnIdleEnd>
      <RestartOnIdle>false</RestartOnIdle>
    </IdleSettings>
    <AllowStartOnDemand>true</AllowStartOnDemand>
    <Enabled>true</Enabled>
    <Hidden>false</Hidden>
    <RunOnlyIfIdle>false</RunOnlyIfIdle>
    <DisallowStartOnRemoteAppSession>false</DisallowStartOnRemoteAppSession>
    <UseUnifiedSchedulingEngine>true</UseUnifiedSchedulingEngine>
    <WakeToRun>false</WakeToRun>
    <ExecutionTimeLimit>PT0S</ExecutionTimeLimit>
    <Priority>7</Priority>
  </Settings>
  <Actions Context="Author">
    <Exec>
      <Command>\path\to\the\git-local-backup.exe</Command> <!-- Replace with your executable path -->
      <Arguments>--projects-dir "~/Projects" --backup-dir "~/OneDrive/Backup/Projects"</Arguments>
    </Exec>
  </Actions>
</Task>
```

2. Open `Task Scheduler` and import this task via `Action > Import Task` from the top menu bar
3. Check both `Run whether user is logged on or not` and `Do not store password` option.
Otherwise, a terminal window will pop up on each run.
4. To test it, manually run the task from `ni554n` folder
5. Refresh the task list and check the `Last Run Result` column to see if it's a successful run

</details>

## Information

**Author:** [Nissan Ahmed](https://anissan.com) ([@ni554n](https://twitter.com/ni554n))

**Donate:** [PayPal](https://paypal.me/ni554n)
<img src="https://ping.anissan.com/?repo=git-local-backup" width="0" height="0" align="right">
