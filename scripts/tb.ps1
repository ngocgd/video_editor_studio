#!/usr/bin/env pwsh
# Runs a Makefile target inside the toolbox container so the host never
# needs Go, buf, sqlc, or any other Go tool installed directly.
param(
  [Parameter(ValueFromRemainingArguments = $true)]
  [string[]]$Args
)

$ErrorActionPreference = "Stop"
Set-Location (Join-Path $PSScriptRoot "..")

& docker compose -f deploy/compose.tools.yml run --rm toolbox @Args
exit $LASTEXITCODE
