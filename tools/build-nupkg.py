#!/usr/bin/env python3
"""build-nupkg.py [out_dir] - build hgit-native.<version>.nupkg from
packaging/chocolatey (a nupkg is an OPC zip: nuspec + tools/ + the three OPC
bookkeeping parts). Needed because `choco pack` only runs on Windows.
Mirrors hgit's own tools/build-nupkg.py exactly."""
import glob, os, re, sys, uuid, zipfile
here = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "packaging", "chocolatey")
out = sys.argv[1] if len(sys.argv) > 1 else "dist"
os.makedirs(out, exist_ok=True)
nuspec_path = glob.glob(os.path.join(here, "*.nuspec"))[0]
pkg_id = os.path.splitext(os.path.basename(nuspec_path))[0]
nuspec = open(nuspec_path, encoding="utf-8").read()
ver = re.search(r"<version>([^<]+)</version>", nuspec).group(1)
path = os.path.join(out, "%s.%s.nupkg" % (pkg_id, ver))
psm = "package/services/metadata/core-properties/%s.psmdcp" % uuid.uuid4().hex
with zipfile.ZipFile(path, "w", zipfile.ZIP_DEFLATED) as z:
    z.writestr("[Content_Types].xml", '<?xml version="1.0" encoding="utf-8"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml" /><Default Extension="nuspec" ContentType="application/octet" /><Default Extension="ps1" ContentType="application/octet" /><Default Extension="psmdcp" ContentType="application/vnd.openxmlformats-package.core-properties+xml" /></Types>')
    z.writestr("_rels/.rels", '<?xml version="1.0" encoding="utf-8"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Type="http://schemas.microsoft.com/packaging/2010/07/manifest" Target="/%s.nuspec" Id="R1" /><Relationship Type="http://schemas.openxmlformats.org/package/2006/relationships/metadata/core-properties" Target="/%s" Id="R2" /></Relationships>' % (pkg_id, psm))
    z.writestr(psm, '<?xml version="1.0" encoding="utf-8"?><coreProperties xmlns="http://schemas.openxmlformats.org/package/2006/metadata/core-properties"><creator>VectorSophie</creator><description>%s</description><identifier>%s</identifier><version>%s</version><keywords>hgit vcs cli</keywords><lastModifiedBy>build-nupkg.py</lastModifiedBy></coreProperties>' % (pkg_id, pkg_id, ver))
    z.write(nuspec_path, "%s.nuspec" % pkg_id)
    for f in sorted(os.listdir(os.path.join(here, "tools"))):
        z.write(os.path.join(here, "tools", f), "tools/" + f)
print(path)
