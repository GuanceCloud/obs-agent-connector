# QwenWork CN Windows RC acceptance

This checklist is for the `v0.1.31-rc1` release candidate.
It is not a publication announcement. Windows AMD64 and ARM64 runtime acceptance
is pending. Cross-compilation and macOS fixtures do not establish Windows desktop,
shell, filesystem, antivirus, or enterprise-policy compatibility.

Use the official tag-reproducible RC for acceptance. Earlier packages built from
the uncommitted working tree are local previews, not the official RC. Record the
artifact version and checksum; do not conflate preview and RC results.

## Prepare and verify the package

Use a disposable Windows account or VM with the domestic QwenWork client. Start
the client once to create `%USERPROFILE%\.qwenworkcn`. Record Windows version,
native architecture, PowerShell version and QwenWork version. Do not use private
conversations, change enterprise policy, enable raw SDK logging or replay history.

Obtain the matching ZIP and `SHA256SUMS` from the same approved versioned release
or supplied local package set, not a stable CDN/latest URL. Choose `amd64` for
x64 Windows or `arm64` for native ARM64 Windows. Do not silently substitute an
emulated AMD64 run for native ARM64 acceptance.

In PowerShell, from the downloaded package directory:

```powershell
$Arch = 'amd64' # Use arm64 on native ARM64 Windows.
$ArchiveName = "obs-agent-connector-windows-$Arch.zip"
$Entries = @(Get-Content -LiteralPath .\SHA256SUMS | Where-Object {
    $_ -match ('^[0-9a-fA-F]{64}\s+\*?' + [regex]::Escape($ArchiveName) + '$')
})
if ($Entries.Count -ne 1) { throw 'Expected exactly one archive checksum' }
$Expected = ($Entries[0] -split '\s+')[0]
$Actual = (Get-FileHash -LiteralPath $ArchiveName -Algorithm SHA256).Hash
if ($Actual -ne $Expected) { throw 'Archive checksum mismatch' }
Add-Type -AssemblyName System.IO.Compression.FileSystem
$Zip = [IO.Compression.ZipFile]::OpenRead((Resolve-Path -LiteralPath $ArchiveName).Path)
try {
    $Names = @($Zip.Entries | ForEach-Object { $_.FullName })
    if ($Names.Count -ne 1 -or $Names[0] -ne "obs-agent-connector-windows-$Arch.exe") {
        throw 'Unexpected archive contents'
    }
} finally { $Zip.Dispose() }
$RunDir = Join-Path $env:LOCALAPPDATA ("Qwen RC test " + [guid]::NewGuid().ToString('N'))
Expand-Archive -LiteralPath $ArchiveName -DestinationPath $RunDir
$Connector = Join-Path $RunDir 'obs-agent-connector.exe'
Rename-Item -LiteralPath (Join-Path $RunDir "obs-agent-connector-windows-$Arch.exe") -NewName 'obs-agent-connector.exe'
& $Connector version --offline
if ($LASTEXITCODE -ne 0) { throw 'Version command failed' }
```

Require the agreed RC version in the output. Keep the executable in this stable
location during testing; do not replace the global PATH executable or run
`version -u`. Repeat with a Windows account/profile path containing non-ASCII
characters and spaces; a Unicode extraction directory alone does not test a
Unicode profile. Record any security-policy block rather than bypassing it.

## Lifecycle and native collection

Use only synthetic conversations and the approved test receiver. Configure shared
connector endpoint/token defaults through the normal installation process before
the commands below, or provide the documented `--endpoint` and `--x-token` flags
locally. Never attach credentials, config files or unredacted shell history to the
acceptance report. Back up profile settings, dotenv and any existing managed config
securely before starting. Fresh-install/removal tests belong in the disposable
account; do not delete real-user state to produce a clean baseline.

```powershell
& $Connector install qwenwork-cn
& $Connector status qwenwork-cn
& $Connector update qwenwork-cn
& $Connector disable qwenwork-cn
& $Connector status qwenwork-cn
& $Connector enable qwenwork-cn
```

After a fresh install, restart the desktop normally and start a new synthetic
conversation. Check one text reply, a multi-call tool turn, an expected tool
failure and a supported Skill invocation/read. Thinking stays disabled by default;
test opt-in only in an isolated scope and restore the prior value afterward.

Check `settings.json` locally: each supported event should have one managed
command with quoted executable/profile paths. Unrelated settings and Hook handlers,
including handlers in the same group, must survive. `update` uses `--no-config`:
compare config and dotenv hashes before/after and confirm Agent identity, thinking
choice and completed checkpoints remain unchanged. Do not post their contents.
Repeat update to verify no duplicate registrations. Disable should stop collection
of a new synthetic turn; enable should resume for another new turn. Previously
pending work is not proof that a disabled new turn was collected.

For retry acceptance, use a controlled loopback receiver to reject one signal,
restore it, and cause a later supported replay trigger. Verify only the failed
signal retries; do not deliberately disrupt a production endpoint. Do not fabricate
terminal cancellation from text, elapsed time, process exit or SessionEnd. If a
safe native terminal sample is unavailable, mark that scenario unverified.

```powershell
& $Connector remove qwenwork-cn --yes
```

Default removal should unregister only managed handlers and remove the managed
Agent directory; user profile settings, dotenv and other Agents must survive.
Reinstall for continued synthetic tests if needed. Do not use broad filesystem
deletion or purge user-owned configuration as part of acceptance.

## Result checklist

Record each row as pass, fail or unverified, with architecture/version and evidence
level. A failure in correctness, privacy, deduplication or packaging blocks the RC.

| Scenario | Required observation | Result |
| --- | --- | --- |
| Package | Checksum, single expected EXE, offline RC version | Pending |
| Fresh install | Quoted Hooks, token flag enabled, unrelated settings preserved | Pending |
| Upgrade twice | Config/dotenv/identity/checkpoints preserved, no duplicate Hooks | Pending |
| Disable/enable | No disabled new-turn upload; enabled new turn collected | Pending |
| Unicode/space paths | Actual desktop invokes Hooks with the correct profile | Pending |
| Text and tools | Parentage, output order, tool failure status, no duplicate signals | Pending |
| Usage | Observed per-call counts match backend metrics; missing is not zero | Pending |
| Skill and thinking | Proven Skill child; thinking opt-in only, content-none dominates | Pending |
| Retry/recovery | Failed signal retries without repeating accepted signal | Pending |
| Removal | Only managed registration/files removed | Pending |
| Native ARM64 | Native ARM64 binary and desktop test, not emulation | Pending |
| Enterprise policy | Separate authorized enterprise-account test | Unverified |

Report emitted/decoded OTLP, HTTP acceptance, independently queried backend storage
and UI rendering separately. `status` is offline: accepted does not prove storage;
the profile flag does not prove the desktop worker inherited it. Share only safe
versions, counts, timestamps, outcome classifications and sanitized screenshots.
