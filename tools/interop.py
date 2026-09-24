#!/usr/bin/env python3
"""interop.py <bundle.qcow2> <hgs_file> <meta_file> - pillar C, the one manual
step ARCHITECTURE.md leaves outstanding: push a repo hgit-native wrote (its
two files) into a real TempleOS guest over COM2, and run Hgit("check ...")
and Hgit("history ...") there. Proves TempleOS can read what hgit-native
wrote, not just the reverse (which every other test in this repo already
covers). Reuses the base hgit repo's build-bundle.py COM2 push receiver and
gen-fixtures.py's boot sequence.

Usage: interop.py <bundle.qcow2> <repo.hgs> <repo.hgs.m> [dest_name]
  dest_name: the C:/Home/<name>.hgs the guest saves to (default: Interop)
"""
import os, shutil, socket, subprocess, sys, time

bundle, hgs_path, m_path = sys.argv[1], sys.argv[2], sys.argv[3]
dest = sys.argv[4] if len(sys.argv) > 4 else "Interop"
work = "/tmp/hgit-interop-work"
os.makedirs(work, exist_ok=True)
disk, serial = work + "/disk.qcow2", work + "/serial.log"
mon_sock, com2_sock = work + "/qemu.sock", work + "/com2.sock"
for f in (serial, mon_sock, com2_sock):
    if os.path.exists(f): os.remove(f)
shutil.copy(bundle, disk)

hgs_bytes = open(hgs_path, "rb").read()
m_bytes = open(m_path, "rb").read()

KEYMAP = {" ": "spc", "\n": "ret", "`": "grave_accent", "-": "minus", "=": "equal", "[": "bracket_left", "]": "bracket_right", "\\": "backslash", ";": "semicolon", "'": "apostrophe", ",": "comma", ".": "dot", "/": "slash"}
SHIFTED = {"!": "1", "@": "2", "#": "3", "$": "4", "%": "5", "^": "6", "&": "7", "*": "8", "(": "9", ")": "0", "_": "minus", "+": "equal", "{": "bracket_left", "}": "bracket_right", "|": "backslash", ":": "semicolon", '"': "apostrophe", "<": "comma", ">": "dot", "?": "slash", "~": "grave_accent"}
def mon():
    s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM); s.connect(mon_sock); time.sleep(0.2); s.recv(4096); return s
def sendkey(s, k): s.send(("sendkey " + k + "\n").encode()); time.sleep(0.05); s.recv(4096)
def type_line(text, wait=1.5):
    s = mon()
    for ch in text:
        k = KEYMAP.get(ch) or ("shift-" + SHIFTED[ch] if ch in SHIFTED else ("shift-" + ch.lower() if ch.isupper() else ch))
        sendkey(s, k)
    sendkey(s, "ret"); time.sleep(wait); s.close()
def log(): return open(serial, "rb").read().decode("latin1")
def wait_for(marker, timeout):
    t = time.time()
    while time.time() - t < timeout:
        if marker in log(): return True
        time.sleep(2)
    return False
def push(payload_bytes):
    # Binary .hgs/.hgs.m bytes legitimately contain 0x04 (e.g. the format
    # version byte at header offset 4) - the same byte used elsewhere as the
    # push protocol's EOT marker. Hex-encode so the wire stream is pure
    # ASCII and 0x04 can only ever mean "done"; the guest decodes pairs back
    # to bytes with HexDigit (already loaded via HgitAll.HC). Small chunks,
    # real pacing: an unpaced burst can silently drop bytes on the emulated
    # serial line (the same failure the base repo's own build-bundle.py
    # documents and works around).
    hex_bytes = payload_bytes.hex().encode()
    c = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM); c.connect(com2_sock); time.sleep(0.3)
    for i in range(0, len(hex_bytes), 64):
        c.sendall(hex_bytes[i:i + 64]); time.sleep(0.05)
    time.sleep(1.0); c.sendall(b"\x04"); time.sleep(1.0); c.close()

q = subprocess.Popen(["qemu-system-x86_64", "-machine", "pc", "-m", "512", "-display", "none",
    "-serial", "file:" + serial, "-monitor", "unix:%s,server,nowait" % mon_sock,
    "-chardev", "socket,id=com2,path=%s,server=on,wait=off" % com2_sock, "-serial", "chardev:com2",
    "-boot", "c", "-drive", "file=%s,if=ide,format=qcow2" % disk])
try:
    for _ in range(50):
        if os.path.exists(mon_sock): break
        time.sleep(0.2)
    time.sleep(4); s = mon(); sendkey(s, "1"); s.close()
    time.sleep(20); s = mon(); sendkey(s, "n"); s.close()
    print("settling 240s ..."); time.sleep(240)
    type_line('#include "::/Doc/Comm";')
    type_line('U8 *Db=MAlloc(524288);I64 Di=0;U8 Dc;Bool _D_exit=FALSE;')
    type_line('CommInit8n1(2,115200);CommInit8n1(1,115200);FifoU8Del(comm_ports[2].RX_fifo);comm_ports[2].RX_fifo=FifoU8New(524288);')
    type_line('I64 sz;U8 *hb=FileRead("C:/Home/HgitAll.HC",&sz);ExePutS(hb);', wait=45)
    type_line('U8 *Bo=MAlloc(262144);I64 BoLen=0;'
              'U0 D(U8 *path){CommPrint(1,"D_OK\\n");while(!_D_exit){if(FifoU8Rem(comm_ports[2].RX_fifo,&Dc)){'
              'if(Dc==4){I64 hi;for(hi=0;hi+1<Di;hi+=2)Bo[BoLen++]=(HexDigit(Db[hi])<<4)|HexDigit(Db[hi+1]);'
              'FileWrite(path,Bo,BoLen);CommPrint(1,"D_DONE %d\\n",BoLen);Di=0;BoLen=0;_D_exit=TRUE;}'
              'else if(Di<524287){Db[Di++]=Dc;}}else Sleep(10);}}')

    for label, payload, dest_path in (
        ("Interop.hgs", hgs_bytes, "C:/Home/%s.hgs" % dest),
        ("Interop.hgs.m", m_bytes, "C:/Home/%s.hgs.m" % dest),
    ):
        for attempt in range(1, 4):
            mark = len(log())
            type_line('_D_exit=FALSE;Di=0;FifoU8Del(comm_ports[2].RX_fifo);comm_ports[2].RX_fifo=FifoU8New(524288);D("%s");' % dest_path)
            if not wait_for("D_OK", 30): sys.exit("receiver never came up for " + label)
            print("pushing %s (%d bytes), attempt %d ..." % (label, len(payload), attempt))
            push(payload)
            if wait_for("D_DONE %d" % len(payload), 30):
                print(label + " saved in guest")
                break
            print("attempt %d short/dropped, retrying; tail:\n%s" % (attempt, log()[mark:][-300:]))
        else:
            sys.exit(label + " never confirmed saved after 3 attempts; serial tail:\n" + log()[-1000:])

    type_line('Hgit("interactive");')
    type_line('Hgit("check %s");' % dest_path.replace(".hgs.m", ".hgs"), wait=4)
    type_line('Hgit("history %s");' % dest_path.replace(".hgs.m", ".hgs"), wait=4)
    time.sleep(3)
    s = mon(); s.send(b"quit\n"); time.sleep(1)
finally:
    try: q.wait(timeout=20)
    except Exception: q.kill()

L = log()
print("\n--- serial output after CHECK/HISTORY ---")
i = L.rfind("D_DONE")
print(L[i:] if i >= 0 else L[-2000:])
