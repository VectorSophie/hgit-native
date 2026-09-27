$ErrorActionPreference = 'Stop'
$toolsDir = Split-Path -Parent $MyInvocation.MyCommand.Definition
Install-ChocolateyZipPackage -PackageName 'hgit-native' `
  -Url 'https://github.com/VectorSophie/hgit-native/releases/download/v1.9.0/hgit-native-1.9.0-windows-amd64.zip' `
  -UnzipLocation $toolsDir -Checksum 'f7ff253fb952fc423cbc4be9d95861cabc5f4fbef50cb68e48a1f3d5b9d2dfb4' -ChecksumType 'sha256'
