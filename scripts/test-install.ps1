$ErrorActionPreference = "Stop"

. (Join-Path $PSScriptRoot "install.ps1")

function Assert-ThrowsWithMessage {
  param(
    [scriptblock]$Action,
    [string]$ExpectedMessage
  )

  try {
    & $Action
  }
  catch {
    if ($_.Exception.Message -notlike "*$ExpectedMessage*") {
      throw "Expected error containing '$ExpectedMessage', got '$($_.Exception.Message)'"
    }
    return
  }
  throw "Expected action to fail with an error containing '$ExpectedMessage'"
}

$UnicodeUserName = -join [char[]](0x6D4B, 0x8BD5, 0x7528, 0x6237)

Assert-TagAssignments -Assignments @(
  "user_id=user-redacted",
  "user_name=$UnicodeUserName",
  "env=prod"
)
Assert-TagAssignments -Assignments @("regions=us-east,us-west")

Assert-ThrowsWithMessage {
  Assert-TagAssignments -Assignments @("user_id=user-redacted,user_name=$UnicodeUserName,env=prod")
} "comma-separated tag assignments are ambiguous"

Assert-ThrowsWithMessage {
  Assert-TagAssignments -Assignments @("missing-value-separator")
} "tag must use KEY=VALUE format"

Write-Host "PowerShell installer tag contract tests passed."
