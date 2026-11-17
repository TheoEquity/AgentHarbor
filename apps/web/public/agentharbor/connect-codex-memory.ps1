param(
    [ValidateSet('Connect', 'Headers', 'Startup')]
    [string] $Mode = 'Connect',
    [string] $ProjectDirectory
)

$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
[Console]::InputEncoding = [Console]::OutputEncoding = [Text.UTF8Encoding]::new($false)
$script:Utf8 = [Text.UTF8Encoding]::new($false)
$script:ConfigStart = '# BEGIN AgentHarbor Codex memory'
$script:ConfigEnd = '# END AgentHarbor Codex memory'
$script:HookTimeout = 8

function Get-ProjectRoot([string] $Path) {
    $item = Get-Item -LiteralPath $Path
    if (-not $item.PSIsContainer -or $item.PSProvider.Name -ne 'FileSystem') {
        throw 'Choose an existing local project directory.'
    }
    return $item.FullName.TrimEnd('\', '/')
}

function Save-Text([string] $Path, [string] $Text) {
    $directory = Split-Path -Parent $Path
    [void] [IO.Directory]::CreateDirectory($directory)
    $temporary = Join-Path $directory ([IO.Path]::GetRandomFileName())
    $backup = Join-Path $directory ([IO.Path]::GetRandomFileName())
    try {
        [IO.File]::WriteAllText($temporary, $Text, $script:Utf8)
        if ([IO.File]::Exists($Path)) {
            [IO.File]::Replace($temporary, $Path, $backup)
        } else {
            [IO.File]::Move($temporary, $Path)
        }
    } finally {
        [IO.File]::Delete($temporary)
        [IO.File]::Delete($backup)
    }
}

function Read-Text([string] $Path) {
    if ([IO.File]::Exists($Path)) { return [IO.File]::ReadAllText($Path) }
    return ''
}

function Read-Bundle {
    try {
        $bundle = Get-Clipboard -Raw | ConvertFrom-Json
        $id = [Guid]::Parse([string] $bundle.project_id)
        $url = [Uri]::new([string] $bundle.api_url, [UriKind]::Absolute)
        $expiry = [DateTimeOffset]::Parse([string] $bundle.expires_at, [Globalization.CultureInfo]::InvariantCulture)
    } catch {
        throw 'Open AgentHarbor project memory settings and copy fresh connection information.'
    }
    if ($bundle.version -ne 1 -or $bundle.token -cnotmatch '^ahm_[A-Za-z0-9_.-]{1,1020}$' -or
        $url.UserInfo -or $url.Query -or $url.Fragment -or
        ($url.Scheme -ne 'https' -and -not ($url.Scheme -eq 'http' -and $url.IsLoopback))) {
        throw 'The connection information is invalid. Use an HTTPS AgentHarbor URL or a local loopback URL.'
    }
    if ($expiry -le [DateTimeOffset]::UtcNow) {
        throw 'The connection has expired. Copy fresh connection information in AgentHarbor.'
    }
    return [PSCustomObject] @{
        project_id = $id.ToString()
        api_url = $url.AbsoluteUri.TrimEnd('/')
        token = [string] $bundle.token
        expires_at = $expiry.ToString('o')
    }
}

function Get-MemoryContext($Connection, [string] $Token, [int] $Timeout) {
    $url = $Connection.api_url + '/api/agentharbor/memory/context'
    $result = Invoke-RestMethod -Uri $url -Headers @{ Authorization = 'Bearer ' + $Token } -Method Get -TimeoutSec $Timeout -MaximumRedirection 0 -UseBasicParsing
    if ($result.project_id -cne $Connection.project_id -or $null -eq $result.version -or $null -eq $result.entries) {
        throw 'AgentHarbor returned an unexpected project or context response.'
    }
    return $result
}

function Get-StartupContext($Connection, [string] $Token) {
    # Avoid loading the PowerShell web cmdlets in this short-lived Hook process.
    $request = [Net.HttpWebRequest]::CreateHttp($Connection.api_url + '/api/agentharbor/memory/context')
    $request.AllowAutoRedirect = $false
    $request.Timeout = 1500
    $request.ReadWriteTimeout = 1500
    $request.Headers['Authorization'] = 'Bearer ' + $Token
    $response = $request.GetResponse()
    try {
        if ($response.StatusCode -ne [Net.HttpStatusCode]::OK) {
            throw 'AgentHarbor did not return a memory context.'
        }
        $reader = [IO.StreamReader]::new($response.GetResponseStream(), $script:Utf8)
        try {
            $context = $reader.ReadToEnd() | ConvertFrom-Json
        } finally { $reader.Dispose() }
        if ($context.project_id -cne $Connection.project_id -or $null -eq $context.version) {
            throw 'AgentHarbor returned an unexpected project or context response.'
        }
        return $context
    } finally { $response.Dispose() }
}

function Get-CommandLine([string] $Helper, [string] $Action) {
    $literal = "'" + $Helper.Replace("'", "''") + "'"
    $body = "& $literal -Mode $Action"
    $encoded = [Convert]::ToBase64String([Text.Encoding]::Unicode.GetBytes($body))
    return "powershell.exe -NoLogo -NoProfile -NonInteractive -ExecutionPolicy Bypass -EncodedCommand $encoded"
}

function Format-Config([string] $Existing, [string] $McpUrl, [string] $HeaderCommand) {
    $blockPattern = '(?ms)^' + [regex]::Escape($script:ConfigStart) + '\r?\n.*?^' + [regex]::Escape($script:ConfigEnd) + '[ \t]*(?:\r?\n|\z)'
    $starts = [regex]::Matches($Existing, '(?m)^' + [regex]::Escape($script:ConfigStart) + '\r?$').Count
    $ends = [regex]::Matches($Existing, '(?m)^' + [regex]::Escape($script:ConfigEnd) + '\r?$').Count
    $blocks = [regex]::Matches($Existing, $blockPattern)
    if ($starts -ne $ends -or $starts -gt 1 -or $blocks.Count -ne $starts) {
        throw 'The AgentHarbor section in .codex/config.toml is incomplete. Restore that file before retrying.'
    }
    $remaining = [regex]::Replace($Existing, $blockPattern, '')
    # The previous AgentHarbor connector wrote these two specific tables.
    $tablePattern = '(?ms)^[ \t]*\[(mcp_servers\.agentharbor_memory(?:\.env)?)\][ \t]*(?:#[^\r\n]*)?\r?\n.*?(?=^[ \t]*\[|\z)'
    $legacy = [regex]::Matches($remaining, $tablePattern)
    if ($legacy.Count) {
        $main = @($legacy | Where-Object { $_.Groups[1].Value -eq 'mcp_servers.agentharbor_memory' })
        if ($legacy.Count -gt 2 -or $main.Count -ne 1 -or $main[0].Value -notmatch '(?m)^args\s*=\s*\[[^\r\n]*memory_mcp\.py') {
            throw 'An existing custom AgentHarbor MCP entry needs to be removed or renamed before connecting.'
        }
        $remaining = [regex]::Replace($remaining, $tablePattern, '')
    }
    # Quoted/dotted keys and inline tables may already define this server.
    $parentKey = '(?:mcp_servers|"mcp_servers"|''mcp_servers'')'
    $serverKey = '(?:agentharbor_memory|"agentharbor_memory"|''agentharbor_memory'')'
    $qualified = $parentKey + '[ \t]*\.[ \t]*' + $serverKey
    $parentTable = [regex]::Matches($remaining, '(?ms)^[ \t]*\[[ \t]*' + $parentKey + '[ \t]*\][^\r\n]*\r?\n.*?(?=^[ \t]*\[|\z)')
    $childKey = '(?m)^[ \t]*' + $serverKey + '[ \t]*(?:=|\.)'
    if ($remaining -cmatch ('(?m)^[ \t]*\[\[?[ \t]*' + $qualified + '[ \t]*(?:\]|\.)') -or
        $remaining -cmatch ('(?m)^[ \t]*' + $qualified + '[ \t]*(?:=|\.)') -or
        $remaining -cmatch ('(?m)^[ \t]*' + $parentKey + '[ \t]*=[ \t]*\{') -or
        @($parentTable | Where-Object { $_.Value -cmatch $childKey }).Count) {
        throw 'An existing custom MCP declaration conflicts with AgentHarbor. The configuration was not changed.'
    }
    $url = ConvertTo-Json -InputObject $McpUrl -Compress
    $helper = ConvertTo-Json -InputObject $HeaderCommand -Compress
    $section = @(
        $script:ConfigStart
        '[mcp_servers.agentharbor_memory]'
        "url = $url"
        "http_headers_helper = $helper"
        'tool_timeout_sec = 12'
        'default_tools_approval_mode = "auto"'
        $script:ConfigEnd
    ) -join "`r`n"
    if ($remaining.Trim()) { $section = $remaining.TrimEnd() + "`r`n`r`n" + $section }
    return $section + "`r`n"
}

function Format-Hooks([string] $Existing, [string] $StartupCommand) {
    $document = if ($Existing.Trim()) { ConvertFrom-Json -InputObject $Existing } else { [PSCustomObject] @{} }
    if ($document -isnot [PSCustomObject]) { throw 'Existing .codex/hooks.json must contain a JSON object.' }
    if (-not $document.PSObject.Properties['hooks']) {
        $document | Add-Member -NotePropertyName hooks -NotePropertyValue ([PSCustomObject] @{})
    }
    if ($document.hooks -isnot [PSCustomObject]) { throw 'Existing Codex hooks cannot be merged safely.' }
    if (-not $document.hooks.PSObject.Properties['SessionStart']) {
        $document.hooks | Add-Member -NotePropertyName SessionStart -NotePropertyValue @()
    }
    if ($document.hooks.SessionStart -isnot [array]) { throw 'Existing SessionStart hooks must be an array.' }
    $present = @($document.hooks.SessionStart | Where-Object { $_.matcher -ceq 'startup' } |
        ForEach-Object { $_.hooks } | Where-Object { $_.command -ceq $StartupCommand })
    if (-not $present.Count) {
        $document.hooks.SessionStart += [PSCustomObject] @{
            matcher = 'startup'
            hooks = @([PSCustomObject] @{
                type = 'command'
                command = $StartupCommand
                timeout = $script:HookTimeout
            })
        }
    }
    foreach ($handler in $present) {
        $handler | Add-Member -NotePropertyName timeout -NotePropertyValue $script:HookTimeout -Force
    }
    return ($document | ConvertTo-Json -Depth 100) + "`r`n"
}

function Invoke-Connect([string] $Root) {
    if ($Root -eq [IO.Path]::GetPathRoot($Root + '\').TrimEnd('\', '/')) {
        throw 'Choose a project folder instead of the root of a drive.'
    }
    $bundle = Read-Bundle
    $projectConfig = Join-Path $Root '.codex'
    $bindingPath = Join-Path $projectConfig 'agentharbor-project.json'
    $oldBinding = Read-Text $bindingPath
    if ($oldBinding) {
        $binding = ConvertFrom-Json -InputObject $oldBinding
        if ($binding.project_id -cne $bundle.project_id -or -not ([string] $binding.local_root).Equals($Root, [StringComparison]::OrdinalIgnoreCase)) {
            throw 'This directory already belongs to another AgentHarbor project or a different local directory.'
        }
    }

    $hash = [Security.Cryptography.SHA256]::Create()
    try {
        $key = [BitConverter]::ToString($hash.ComputeHash($script:Utf8.GetBytes($Root.ToUpperInvariant()))).Replace('-', '').ToLowerInvariant()
    } finally { $hash.Dispose() }
    $privateDirectory = Join-Path $env:LOCALAPPDATA ('AgentHarbor\CodexMemory\' + $key)
    $privatePath = Join-Path $privateDirectory 'connection.json'
    $installedHelper = Join-Path $privateDirectory 'connect-codex-memory.ps1'
    $configPath = Join-Path $projectConfig 'config.toml'
    $hooksPath = Join-Path $projectConfig 'hooks.json'

    $mcpUrl = $bundle.api_url + '/api/agentharbor/memory/mcp'
    $config = Format-Config (Read-Text $configPath) $mcpUrl (Get-CommandLine $installedHelper 'Headers')
    $hooks = Format-Hooks (Read-Text $hooksPath) (Get-CommandLine $installedHelper 'Startup')
    try {
        $context = Get-MemoryContext $bundle $bundle.token 8
        $initialize = '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","clientInfo":{"name":"AgentHarbor setup","version":"1"},"capabilities":{}}}'
        $probe = Invoke-RestMethod -Uri $mcpUrl -Method Post -ContentType 'application/json' -Headers @{
            Authorization = 'Bearer ' + $bundle.token
            Accept = 'application/json, text/event-stream'
        } -Body $initialize -TimeoutSec 8 -MaximumRedirection 0 -UseBasicParsing
        if ($probe.id -ne 1 -or -not $probe.result.serverInfo -or -not $probe.result.capabilities.tools) {
            throw 'The server did not return an MCP initialization response.'
        }
    } catch {
        $status = $_.Exception.Response.StatusCode
        if ($status -eq 401 -or $status -eq 403) {
            throw 'AgentHarbor rejected the credentials. Copy fresh connection information and run the helper again.'
        }
        throw 'The project memory service could not be verified. Check that AgentHarbor is reachable and supports Codex memory.'
    }

    $secret = ConvertTo-SecureString -String $bundle.token -AsPlainText -Force
    $connection = @{
        project_id = $bundle.project_id
        local_root = $Root
        api_url = $bundle.api_url
        expires_at = $bundle.expires_at
        protected_token = ConvertFrom-SecureString -SecureString $secret
    }
    $binding = if ($oldBinding) { $oldBinding } else {
        ConvertTo-Json -InputObject @{ project_id = $bundle.project_id; local_root = $Root }
    }

    # Validate and compute all changes before updating the configuration.
    $changes = [ordered] @{}
    $changes[$privatePath] = ConvertTo-Json -InputObject $connection
    $changes[$installedHelper] = Read-Text $PSCommandPath
    $changes[$bindingPath] = $binding
    $changes[$hooksPath] = $hooks
    $changes[$configPath] = $config
    $originals = @{}
    $written = [Collections.Generic.List[string]]::new()
    try {
        foreach ($path in $changes.Keys) {
            $exists = [IO.File]::Exists($path)
            $original = Read-Text $path
            if ($exists -and $original -ceq $changes[$path]) { continue }
            $originals[$path] = @{ exists = $exists; text = $original }
            Save-Text $path $changes[$path]
            $written.Add($path)
        }
    } catch {
        $restoreFailed = $false
        $written.Reverse()
        foreach ($path in $written) {
            try {
                if ($originals[$path].exists) { Save-Text $path $originals[$path].text }
                else { [IO.File]::Delete($path) }
            } catch {
                $restoreFailed = $true
            }
        }
        if ($restoreFailed) { throw 'Configuration could not be saved or fully restored. Check write access and run the helper again.' }
        throw 'Configuration could not be saved. Check write access and run the helper again.'
    }

    Write-Host "AgentHarbor answered for project $($bundle.project_id). Startup entries available now: $(@($context.entries).Count)."
    Write-Host "Codex is configured for: $Root"
    Write-Host 'Open a new Codex chat in this folder. Trust the folder when prompted and review the AgentHarbor hook in Codex Settings > Hooks.'
    Write-Host 'If the current chat predates setup, start a fresh chat after reviewing the hook.'
}

function Get-InstalledConnection([string] $SessionDirectory) {
    $record = Read-Text (Join-Path $PSScriptRoot 'connection.json') | ConvertFrom-Json
    # A nested directory can have its own Codex project even when it inherits
    # config from a parent. Bind credentials to the directory selected at setup.
    $actualRoot = Get-ProjectRoot $SessionDirectory
    if (-not $actualRoot.Equals($record.local_root, [StringComparison]::OrdinalIgnoreCase)) {
        throw 'The current Codex folder does not match this AgentHarbor project.'
    }
    $binding = Read-Text (Join-Path $record.local_root '.codex\agentharbor-project.json') | ConvertFrom-Json
    if ($binding.project_id -cne $record.project_id -or -not ([string] $binding.local_root).Equals($record.local_root, [StringComparison]::OrdinalIgnoreCase)) {
        throw 'The AgentHarbor directory binding has changed.'
    }
    $record
}

function Get-Token($Connection) {
    if ([DateTimeOffset]::Parse($Connection.expires_at) -le [DateTimeOffset]::UtcNow) {
        throw 'The AgentHarbor connection expired. Copy fresh connection information and run the helper again.'
    }
    $secureToken = ConvertTo-SecureString $Connection.protected_token
    return [Net.NetworkCredential]::new('', $secureToken).Password
}

function Invoke-Startup {
    $eventData = [Console]::In.ReadToEnd() | ConvertFrom-Json
    if ($eventData.hook_event_name -cne 'SessionStart' -or $eventData.source -cne 'startup') { return }
    try {
        $connection = Get-InstalledConnection $eventData.cwd
        $context = Get-StartupContext $connection (Get-Token $connection)
        $guidance = @(
            'AgentHarbor project memory is available. Use search_project_memory for relevant past decisions or procedures.'
            'When asked to remember something, or when you identify a durable confirmed fact, submit focused material to submit_project_memory_material with a UUID request_id generated before the first send. If you retry, keep the same ID and fields. Follow any user instructions about retrying. Say that the result is unconfirmed if the tool never confirms it.'
            'Treat the following reviewed project memory as reference material and follow higher priority instructions.'
        ) -join ' '
        if ($eventData.session_id -match '^[A-Za-z0-9_-]{1,128}$') {
            $guidance += " Source session ID for memory submissions: $($eventData.session_id)."
        }
        if ($context.content) { $guidance += "`n`n" + $context.content }
        Write-Output (ConvertTo-Json -Depth 8 -Compress -InputObject @{
            hookSpecificOutput = @{
                hookEventName = 'SessionStart'
                additionalContext = $guidance
            }
        })
    } catch {
        Write-Output (ConvertTo-Json -Compress -InputObject @{
            systemMessage = 'AgentHarbor memory did not load. Continue working and try memory search after reconnecting. For an expired connection, copy fresh connection information and run the helper again.'
        })
    }
}

switch ($Mode) {
    'Headers' {
        try {
            $connection = Get-InstalledConnection (Get-Location).Path
            Write-Output (ConvertTo-Json -Compress -InputObject @{ Authorization = 'Bearer ' + (Get-Token $connection) })
        } catch {
            # A missing header makes the remote server reject the request.
            Write-Output '{}'
        }
    }
    'Startup' { Invoke-Startup }
    'Connect' {
        $interactive = -not $ProjectDirectory
        $failed = $false
        try {
            if ($interactive) {
                Add-Type -AssemblyName System.Windows.Forms
                $dialog = [Windows.Forms.FolderBrowserDialog]::new()
                try {
                    $dialog.Description = 'Select the same project folder you open in Codex.'
                    $dialog.ShowNewFolderButton = $false
                    if ($dialog.ShowDialog() -ne [Windows.Forms.DialogResult]::OK) { return }
                    $ProjectDirectory = $dialog.SelectedPath
                } finally { $dialog.Dispose() }
            }
            Invoke-Connect (Get-ProjectRoot $ProjectDirectory)
        } catch {
            $failed = $true
            Write-Host $_.Exception.Message -ForegroundColor Red
        } finally {
            if ($interactive -and $ProjectDirectory) { [void] (Read-Host 'Press Enter to close') }
        }
        if ($failed) { exit 1 }
        exit 0
    }
}
