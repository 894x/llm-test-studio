param(
    [string]$ServerUrl = 'http://127.0.0.1:3000',
    [string]$PricingFile = (Join-Path $PSScriptRoot '..\docs\tencent-tokenhub-video-pricing.json'),
    [switch]$Apply
)

$ErrorActionPreference = 'Stop'
$ServerUrl = $ServerUrl.TrimEnd('/')
$pricing = Get-Content -Raw -LiteralPath $PricingFile | ConvertFrom-Json
$session = [Microsoft.PowerShell.Commands.WebRequestSession]::new()
$headers = @{}

if (-not [string]::IsNullOrWhiteSpace($env:TENCENT_TOKENHUB_ADMIN_ACCESS_TOKEN)) {
    if ([string]::IsNullOrWhiteSpace($env:TENCENT_TOKENHUB_ADMIN_USER_ID)) {
        throw 'Set TENCENT_TOKENHUB_ADMIN_USER_ID when using TENCENT_TOKENHUB_ADMIN_ACCESS_TOKEN.'
    }
    $headers['Authorization'] = "Bearer $($env:TENCENT_TOKENHUB_ADMIN_ACCESS_TOKEN)"
    $headers['New-Api-User'] = $env:TENCENT_TOKENHUB_ADMIN_USER_ID
} else {
    if ([string]::IsNullOrWhiteSpace($env:TENCENT_TOKENHUB_ADMIN_USERNAME) -or
        [string]::IsNullOrWhiteSpace($env:TENCENT_TOKENHUB_ADMIN_PASSWORD)) {
        throw 'Set the Tencent TokenHub admin access-token variables or username/password variables before running this script.'
    }
    $loginBody = @{
        username = $env:TENCENT_TOKENHUB_ADMIN_USERNAME
        password = $env:TENCENT_TOKENHUB_ADMIN_PASSWORD
    } | ConvertTo-Json -Compress
    $login = Invoke-RestMethod -Method Post -Uri "$ServerUrl/api/user/login?turnstile=" `
        -WebSession $session -ContentType 'application/json' -Body $loginBody
    if (-not $login.success -or $null -eq $login.data.id) {
        throw "Login failed: $($login.message)"
    }
    $headers['New-Api-User'] = [string]$login.data.id
}

function Read-NumberMap([string]$value, [string]$key) {
    if ([string]::IsNullOrWhiteSpace($value)) { return @{} }
    try {
        $object = $value | ConvertFrom-Json -AsHashtable
    } catch {
        throw "The current $key value is not a JSON object: $($_.Exception.Message)"
    }
    if ($object -isnot [hashtable]) {
        throw "The current $key value is not a JSON object."
    }
    return $object
}

function Read-Options {
    $response = Invoke-RestMethod -Method Get -Uri "$ServerUrl/api/option/" -WebSession $session -Headers $headers
    if (-not $response.success) {
        throw "Reading current options failed: $($response.message)"
    }
    $values = @{}
    foreach ($option in $response.data) {
        $values[[string]$option.key] = [string]$option.value
    }
    return $values
}

$optionValues = Read-Options
$expectedExchangeRate = [double]$pricing.usd_exchange_rate
$actualExchangeRate = [double]$optionValues['USDExchangeRate']
if ([Math]::Abs($actualExchangeRate - $expectedExchangeRate) -gt 0.0000001) {
    throw "USDExchangeRate is $actualExchangeRate, but this pricing file requires $expectedExchangeRate."
}

$modelPrices = Read-NumberMap $optionValues['ModelPrice'] 'ModelPrice'
$targetNames = @()
foreach ($property in $pricing.models.PSObject.Properties) {
    $targetNames += $property.Name
    $modelPrices[$property.Name] = [double]$property.Value.model_price_usd
}
$modelPriceJson = $modelPrices | ConvertTo-Json -Compress -Depth 20

if ($Apply) {
    $update = @{ key = 'ModelPrice'; value = $modelPriceJson }
    $result = Invoke-RestMethod -Method Put -Uri "$ServerUrl/api/option/" -WebSession $session `
        -Headers $headers -ContentType 'application/json' -Body ($update | ConvertTo-Json -Compress -Depth 20)
    if (-not $result.success) {
        throw "Updating ModelPrice failed: $($result.message)"
    }
}

$verifiedValues = Read-Options
$verifiedPrices = Read-NumberMap $verifiedValues['ModelPrice'] 'ModelPrice'
$failures = @()
foreach ($property in $pricing.models.PSObject.Properties) {
    $name = $property.Name
    $expected = [double]$property.Value.model_price_usd
    if (-not $verifiedPrices.ContainsKey($name) -or
        [Math]::Abs(([double]$verifiedPrices[$name]) - $expected) -gt 0.000000000001) {
        $failures += $name
    }
}
if ($failures.Count -gt 0) {
    throw "Verification failed for: $($failures -join ', ')"
}

$action = if ($Apply) { 'Imported' } else { 'Verified existing' }
Write-Output "$action $($targetNames.Count) Tencent TokenHub video model prices."
