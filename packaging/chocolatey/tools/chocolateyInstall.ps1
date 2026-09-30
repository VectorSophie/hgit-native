$ErrorActionPreference = 'Stop'
$toolsDir = Split-Path -Parent $MyInvocation.MyCommand.Definition
Install-ChocolateyZipPackage -PackageName 'hgit-native' `
  -Url 'https://github.com/VectorSophie/hgit-native/releases/download/v1.9.1/hgit-native-1.9.1-windows-amd64.zip' `
  -UnzipLocation $toolsDir -Checksum 'c63ac182276dc5a19c31311ed3e2831b9bbe8b04b39b73c5ab2c8a7d030596dd' -ChecksumType 'sha256'
