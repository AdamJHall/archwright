#!/usr/bin/env python3
"""Fully-automated VM end-to-end harness for archwright.

For each matrix descriptor this:
  1. builds the binary, creates fresh qcow2 disks, boots the Arch live ISO
     headless (serial on a unix socket);
  2. drives the live-ISO root shell to run `archwright install --yes` (Phase A);
  3. while the target is still reachable from the ISO, injects a *harness-only*
     Phase-B autorun scaffold into the installed system (NOPASSWD sudoers, a
     serial-getty autologin as the user, a .bash_profile trigger, and the
     bootstrap+validate scripts) — none of which lives in any real config;
  4. powers off, reboots from disk, and lets the scaffold run `archwright
     bootstrap` followed by the parametrized validate.sh, watching the serial
     log for the PASS/FAIL markers (Phase B);
  5. reports the result and tears the VM down.

Pure stdlib — no host packages beyond qemu/OVMF/bsdtar. See README in this dir.
"""
from __future__ import annotations

import argparse
import concurrent.futures
import fcntl
import os
import re
import select
import shutil
import socket
import subprocess
import sys
import threading
import time
import uuid

HERE = os.path.dirname(os.path.abspath(__file__))
REPO = os.path.abspath(os.path.join(HERE, "..", "..", ".."))
WORK = os.path.join(REPO, ".e2e")
CACHE = os.path.join(WORK, "cache")

OVMF_CODE = "/usr/share/edk2/x64/OVMF_CODE.4m.fd"
OVMF_VARS = "/usr/share/edk2/x64/OVMF_VARS.4m.fd"
DEFAULT_ISO = os.path.join(REPO, ".iso", "archlinux-2026.06.01-x86_64.iso")

# --- matrix ----------------------------------------------------------------
# Each descriptor fully describes one VM run. Descriptors live one-family-per-file
# under matrix/ (each module exports a `DESCRIPTORS` list) so agents grow coverage
# by adding a new file + configs/<name>.yaml — never editing a shared list — and
# the orchestrator stays generic. A descriptor's keys:
#   name        unique id; also the run-dir + serial-log name.
#   config      path (relative to this file) of the archwright config.yaml.
#   disks       qcow2 sizes, in order -> vda, vdb, vdc, ...
#   user        the config's user.name (must use a bash login shell).
#   phase_b     run Phase B (bootstrap+validate)? False = Phase A / install only.
#   esp_part    the ESP partition device (for reference / assertions).
#   root_mount  commands run AS ROOT on the live ISO (post-install, target
#               unmounted) to remount the installed root at /mnt + ESP at
#               /mnt/boot, so the Phase-B scaffold can be injected. Layout-specific.
#   grub_serial append console=ttyS0 to the installed GRUB cmdline (grub only).
#   expect      EXPECT_* values fed to validate.sh (see lib/validate.sh).
def load_matrix() -> list[dict]:
    import importlib.util
    mdir = os.path.join(HERE, "matrix")
    out: list[dict] = []
    if not os.path.isdir(mdir):
        return out
    for fn in sorted(os.listdir(mdir)):
        if not fn.endswith(".py") or fn.startswith("_"):
            continue
        spec = importlib.util.spec_from_file_location(f"matrix_{fn[:-3]}", os.path.join(mdir, fn))
        mod = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(mod)  # type: ignore[union-attr]
        out.extend(getattr(mod, "DESCRIPTORS", []))
    return out


MATRIX = load_matrix()

# Per-thread run tag so log lines from concurrent VM runs (`--jobs N`) stay
# attributable; the kernel-extraction lock guards the shared CACHE.
_log_ctx = threading.local()
_kernel_lock = threading.Lock()
_print_lock = threading.Lock()


def log(msg: str) -> None:
    tag = getattr(_log_ctx, "tag", "")
    prefix = f"[e2e][{tag}]" if tag else "[e2e]"
    with _print_lock:
        print(f"{prefix} {msg}", flush=True)


# --- serial console --------------------------------------------------------
class Serial:
    """Client for QEMU's serial unix socket with a background reader.

    expect() scans the accumulated decoded output for a regex; run() sends a
    shell command followed by a unique sentinel and waits for `SENTINEL:<rc>`,
    returning the exit code. The sentinel makes command completion detection
    immune to prompt/shell variation (zsh on the ISO, bash on the target).
    """

    def __init__(self, path: str, logfile: str):
        self.path = path
        self.buf = ""
        self.lock = threading.Lock()
        self.closed = False
        self.logf = open(logfile, "a", buffering=1, encoding="utf-8", errors="replace")
        self.sock = self._connect()
        self.reader = threading.Thread(target=self._read_loop, daemon=True)
        self.reader.start()

    def _connect(self, timeout: float = 60) -> socket.socket:
        deadline = time.time() + timeout
        while True:
            try:
                s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
                s.connect(self.path)
                s.setblocking(False)
                return s
            except (FileNotFoundError, ConnectionRefusedError, OSError):
                if time.time() > deadline:
                    raise
                time.sleep(0.2)

    def _read_loop(self) -> None:
        while not self.closed:
            try:
                r, _, _ = select.select([self.sock], [], [], 0.5)
                if not r:
                    continue
                data = self.sock.recv(65536)
                if not data:
                    break
                text = data.decode("utf-8", errors="replace")
                with self.lock:
                    self.buf += text
                self.logf.write(text)
            except OSError:
                break
        self.closed = True

    def send(self, data: str) -> None:
        raw = data.encode()
        while raw:
            try:
                n = self.sock.send(raw)
                raw = raw[n:]
            except (BlockingIOError, InterruptedError):
                time.sleep(0.01)

    def expect(self, pattern: str, timeout: float) -> re.Match:
        rx = re.compile(pattern)
        deadline = time.time() + timeout
        seen = 0
        while True:
            with self.lock:
                m = rx.search(self.buf, seen)
                # Only advance the search start past stable text to keep it cheap.
                seen = max(0, len(self.buf) - 4096)
            if m:
                return m
            if self.closed:
                raise EOFError(f"serial closed while waiting for {pattern!r}")
            if time.time() > deadline:
                raise TimeoutError(f"timed out waiting for {pattern!r}")
            time.sleep(0.2)

    def run(self, cmd: str, timeout: float = 120) -> int:
        tok = uuid.uuid4().hex[:8]
        # The echoed input line contains a literal `$?` (no digit), so the regex
        # `:(\d+)` only matches the evaluated result line, never the echo.
        self.send(f"{cmd}; echo __E2E_{tok}__:$?\n")
        m = self.expect(rf"__E2E_{tok}__:(\d+)", timeout)
        return int(m.group(1))

    def run_ok(self, cmd: str, timeout: float = 120) -> None:
        rc = self.run(cmd, timeout)
        if rc != 0:
            raise RuntimeError(f"command failed (rc={rc}): {cmd}")

    def wait_ready(self, timeout: float = 300) -> None:
        """Block until a real shell answers (autologin completed). If a login
        prompt shows instead of an autologin (archiso autologins root, but be
        defensive), answer it with `root`."""
        deadline = time.time() + timeout
        while True:
            with self.lock:
                if re.search(r"login:\s*$", self.buf):
                    self.send("root\n")
            self.send("\n")
            try:
                if self.run("expr 6 \\* 7 >/dev/null", timeout=8) == 0:
                    return
            except (TimeoutError, EOFError):
                pass
            if time.time() > deadline:
                raise TimeoutError("shell never became ready")

    def close(self) -> None:
        self.closed = True
        try:
            self.sock.close()
        except OSError:
            pass
        self.logf.close()


# --- qemu ------------------------------------------------------------------
def iso_path() -> str:
    return os.environ.get("ARCH_ISO", DEFAULT_ISO)


def ensure_kernel() -> tuple[str, str, str]:
    """Extract vmlinuz/initramfs from the ISO (cached) and return (kernel, initrd, label)."""
    os.makedirs(CACHE, exist_ok=True)
    iso = iso_path()
    if not os.path.isfile(iso):
        sys.exit(f"ISO not found: {iso} (set ARCH_ISO= or run `task iso`)")
    kern = os.path.join(CACHE, "vmlinuz-linux")
    init = os.path.join(CACHE, "initramfs-linux.img")
    # Lock: concurrent runs (--jobs) must not race on the shared extraction.
    with _kernel_lock:
        if not os.path.isfile(kern) or os.path.getmtime(iso) > os.path.getmtime(kern):
            _extract_kernel(iso, kern, init)
    label = subprocess.run(
        ["blkid", "-p", "-s", "LABEL", "-o", "value", iso],
        capture_output=True, text=True,
    ).stdout.strip()
    return kern, init, label


def _extract_kernel(iso: str, kern: str, init: str) -> None:
    log("extracting kernel/initramfs from ISO")
    subprocess.run(
        ["bsdtar", "-xf", iso, "-C", CACHE,
         "arch/boot/x86_64/vmlinuz-linux", "arch/boot/x86_64/initramfs-linux.img"],
        check=True,
    )
    sub = os.path.join(CACHE, "arch", "boot", "x86_64")
    shutil.move(os.path.join(sub, "vmlinuz-linux"), kern)
    shutil.move(os.path.join(sub, "initramfs-linux.img"), init)
    shutil.rmtree(os.path.join(CACHE, "arch"), ignore_errors=True)


def qemu_common(rundir: str, disks: list[str], mem: str, smp: str) -> list[str]:
    vars_fd = os.path.join(rundir, "OVMF_VARS.fd")
    args = [
        "qemu-system-x86_64",
        "-enable-kvm", "-cpu", "host", "-m", mem, "-smp", smp, "-machine", "q35",
        "-no-reboot",
        "-display", "none",
        "-drive", f"if=pflash,format=raw,readonly=on,file={OVMF_CODE}",
        "-drive", f"if=pflash,format=raw,file={vars_fd}",
        "-netdev", "user,id=n0",
        "-device", "virtio-net-pci,netdev=n0",
        "-chardev", f"socket,id=ser0,path={os.path.join(rundir, 'serial.sock')},server=on,wait=off",
        "-serial", "chardev:ser0",
    ]
    for i, _ in enumerate(disks):
        args += ["-drive", f"file={os.path.join(rundir, f'disk{i+1}.qcow2')},if=virtio,format=qcow2"]
    return args


def boot(args: list[str], rundir: str, tag: str) -> subprocess.Popen:
    sock = os.path.join(rundir, "serial.sock")
    if os.path.exists(sock):
        os.unlink(sock)
    log(f"booting QEMU ({tag})")
    return subprocess.Popen(
        args, stdout=subprocess.DEVNULL, stderr=subprocess.STDOUT,
    )


def poweroff_and_wait(con: Serial, proc: subprocess.Popen, timeout: float = 120) -> None:
    try:
        proc.wait(timeout=timeout)
    except subprocess.TimeoutExpired:
        log("VM did not power off in time; killing")
        proc.kill()
        proc.wait()
    con.close()


# --- phases ----------------------------------------------------------------
def write_scaffold(rundir: str, d: dict) -> None:
    """Write the Phase-B scaffold files into the run dir (shared into the VM).

    Everything is materialised host-side and `cp`d into the target during
    inject_phase_b — far more robust than printf-over-serial escaping.
    """
    user = d["user"]
    exp = d["expect"]

    def w(name: str, content: str) -> None:
        with open(os.path.join(rundir, name), "w") as f:
            f.write(content)

    # Parametrized expectations consumed by validate.sh (its sibling).
    w("expect.env", "".join(f'EXPECT_{k}="{v}"\n' for k, v in exp.items()))
    # The validation script defaults to the disk-layout asserter; feature-coverage
    # descriptors point validate_script at lib/features.sh instead. Either reads
    # the same expect.env (its EXPECT_* keys) and prints OK:/FAIL: + an exit code.
    shutil.copy(os.path.join(HERE, d.get("validate_script", "lib/validate.sh")),
                os.path.join(rundir, "validate.sh"))

    # The autorun runner: invoked by the login shell on the installed system.
    w("e2e-bootstrap.sh",
      "#!/usr/bin/env bash\n"
      "set -uo pipefail\n"
      'echo "E2E_PHASEB_START"\n'
      'cd "$HOME"\n'
      "# Wait for first-boot networking (NetworkManager via DHCP).\n"
      "for i in $(seq 1 90); do getent hosts archlinux.org >/dev/null 2>&1 && break; sleep 2; done\n"
      "./archwright bootstrap --config config.yaml; brc=$?\n"
      'echo "E2E_BOOTSTRAP_RC=$brc"\n'
      "bash e2e-validate.sh; vrc=$?\n"
      'echo "E2E_VALIDATE_RC=$vrc"\n'
      'if [ "$brc" = 0 ] && [ "$vrc" = 0 ]; then echo "E2E_RESULT=PASS"; else echo "E2E_RESULT=FAIL"; fi\n'
      'echo "E2E_PHASEB_DONE"\n'
      "sync\n"
      "sudo systemctl poweroff\n")

    # NOPASSWD sudo so bootstrap (sudo) + poweroff run unattended.
    w("sudoers-e2e", f"{user} ALL=(ALL) NOPASSWD: ALL\n")

    # Serial-getty autologin override (logs the user in on ttyS0 at next boot).
    # `-f` in the login-options is essential: it tells login(1) to skip
    # authentication — without it the autologin still prompts for a password and
    # times out.
    w("autologin.conf",
      "[Service]\n"
      "ExecStart=\n"
      f"ExecStart=-/sbin/agetty -o '-p -f -- \\u' --keep-baud --autologin {user} "
      "115200,38400,9600 ttyS0 $TERM\n")

    # Login-shell trigger (guarded to fire exactly once, then powers off inside
    # e2e-bootstrap.sh).
    w("bash_profile",
      "# e2e autorun\n"
      'if [ -z "${E2E_RAN:-}" ] && [ -f "$HOME/e2e-bootstrap.sh" ]; then\n'
      "  export E2E_RAN=1\n"
      '  bash "$HOME/e2e-bootstrap.sh"\n'
      "fi\n")

    # Optional: a tiny local chezmoi source repo to inject into the target, so the
    # dotfiles stage has a deterministic, offline `repo:` to clone+apply (no giant
    # external clone). It applies `dot_e2e-dotfile` -> ~/.e2e-dotfile, which
    # features.sh asserts.
    if d.get("inject_repo"):
        _build_chezmoi_repo(os.path.join(rundir, "injectrepo"))


def _build_chezmoi_repo(path: str) -> None:
    """Create a minimal chezmoi source repo (a git repo with one managed dotfile)."""
    shutil.rmtree(path, ignore_errors=True)
    os.makedirs(path, exist_ok=True)
    with open(os.path.join(path, "dot_e2e-dotfile"), "w") as f:
        f.write("archwright e2e dotfile (applied by chezmoi)\n")
    env = {**os.environ,
           "GIT_AUTHOR_NAME": "e2e", "GIT_AUTHOR_EMAIL": "e2e@example.com",
           "GIT_COMMITTER_NAME": "e2e", "GIT_COMMITTER_EMAIL": "e2e@example.com"}
    subprocess.run(["git", "init", "-q", "-b", "main", path], check=True)
    subprocess.run(["git", "-C", path, "add", "-A"], check=True)
    subprocess.run(["git", "-C", path, "commit", "-q", "-m", "e2e dotfiles"], check=True, env=env)


def inject_phase_b(con: Serial, d: dict) -> None:
    """As root on the live ISO (target remounted at /mnt), install the harness-only
    Phase-B autorun scaffold into the installed system. The run dir is shared
    read-only at /root/e2e; stageBinary already placed archwright + config.yaml in
    the user's home, so we only add the e2e scripts + autologin wiring."""
    user = d["user"]
    home = f"/mnt/home/{user}"
    log("injecting Phase-B scaffold into target")

    # Remount the freshly installed root per the descriptor recipe, then the ESP
    # at /mnt/boot. The orchestrator owns the ESP mount (via esp_part) and creates
    # the mountpoint, because some roots (e.g. a btrfs @ subvol) ship no /boot dir.
    # The mountpoint guard keeps it correct even if a recipe also mounts boot.
    for cmd in d["root_mount"]:
        con.run_ok(cmd, timeout=60)
    con.run_ok(f"mountpoint -q /mnt/boot || {{ mkdir -p /mnt/boot && mount {d['esp_part']} /mnt/boot; }}", timeout=60)

    if d.get("diag"):
        con.run("echo DIAG_DEFAULT; btrfs subvolume get-default /mnt 2>/dev/null; "
                "echo DIAG_AT_HOME; ls -la /mnt/home 2>/dev/null; "
                "echo DIAG_TOP; mkdir -p /mnt-top && mount -o subvolid=5 /dev/vda2 /mnt-top 2>/dev/null; "
                "ls -la /mnt-top 2>/dev/null; echo DIAG_TOP_HOME; ls -la /mnt-top/home 2>/dev/null; "
                "echo DIAG_SUBVOLS; btrfs subvolume list /mnt-top 2>/dev/null; "
                "echo DIAG_FIND; find /mnt-top -maxdepth 5 -name archwright 2>/dev/null; "
                "umount /mnt-top 2>/dev/null; echo DIAG_END", timeout=90)

    # NOPASSWD sudoers.
    con.run_ok("install -d -m 755 /mnt/etc/sudoers.d", timeout=30)
    con.run_ok("cp /root/e2e/sudoers-e2e /mnt/etc/sudoers.d/99-e2e && chmod 440 /mnt/etc/sudoers.d/99-e2e", timeout=30)

    # Serial-getty autologin + enable the unit for next boot.
    con.run_ok("install -d /mnt/etc/systemd/system/serial-getty@ttyS0.service.d", timeout=30)
    con.run_ok("cp /root/e2e/autologin.conf /mnt/etc/systemd/system/serial-getty@ttyS0.service.d/autologin.conf", timeout=30)
    con.run_ok("install -d /mnt/etc/systemd/system/getty.target.wants", timeout=30)
    con.run_ok(
        "ln -sf /usr/lib/systemd/system/serial-getty@.service "
        "/mnt/etc/systemd/system/getty.target.wants/serial-getty@ttyS0.service", timeout=30)

    # Scaffold scripts into the user's home.
    con.run_ok(f"cp /root/e2e/e2e-bootstrap.sh {home}/e2e-bootstrap.sh", timeout=30)
    con.run_ok(f"cp /root/e2e/validate.sh {home}/e2e-validate.sh", timeout=30)
    con.run_ok(f"cp /root/e2e/expect.env {home}/expect.env", timeout=30)
    con.run_ok(f"cp /root/e2e/bash_profile {home}/.bash_profile", timeout=30)
    # Inject the local chezmoi source repo (for the dotfiles descriptor) under the
    # user's home so the configured file:// repo resolves offline.
    if d.get("inject_repo"):
        con.run_ok(f"cp -r /root/e2e/injectrepo /mnt{d['inject_repo']}", timeout=60)
    # chown in the chroot — the live ISO has no such user; only the target does.
    con.run_ok(f"arch-chroot /mnt chown -R {user}:{user} /home/{user}", timeout=60)

    # Best-effort: surface kernel/systemd boot on serial during the disk phase.
    if d.get("grub_serial"):
        con.run(
            "sed -i 's#GRUB_CMDLINE_LINUX_DEFAULT=\"#GRUB_CMDLINE_LINUX_DEFAULT=\"console=ttyS0,115200 #' "
            "/mnt/etc/default/grub && arch-chroot /mnt grub-mkconfig -o /boot/grub/grub.cfg",
            timeout=180)

    # Recursive: handles nested mounts (ESP at /mnt/boot, btrfs @home at /mnt/home).
    con.run("umount -R /mnt", timeout=30)
    con.run("vgchange -an vg0", timeout=30)


def run_descriptor(d: dict, keep: bool, phase_b_only: bool = False) -> bool:
    """Acquire a per-descriptor lock (so two invocations can't collide on the same
    run dir / disks / serial socket — across threads AND separate processes), then
    run it."""
    name = d["name"]
    _log_ctx.tag = name
    os.makedirs(os.path.join(WORK, "runs"), exist_ok=True)
    lockf = open(os.path.join(WORK, "runs", f".{name}.lock"), "w")
    try:
        fcntl.flock(lockf, fcntl.LOCK_EX | fcntl.LOCK_NB)
    except OSError:
        log(f"another run for {name} is already active; skipping "
            f"(don't run the same descriptor concurrently)")
        lockf.close()
        return False
    try:
        return _run_descriptor(d, keep, phase_b_only)
    finally:
        fcntl.flock(lockf, fcntl.LOCK_UN)
        lockf.close()


def _run_descriptor(d: dict, keep: bool, phase_b_only: bool = False) -> bool:
    name = d["name"]
    _log_ctx.tag = name  # attribute this thread's log lines under --jobs
    rundir = os.path.join(WORK, "runs", name)
    mem = d.get("mem", "4G")
    smp = d.get("smp", "4")
    serial_log = os.path.join(rundir, "serial.log")

    # --phase-b-only reuses the already-installed disks from a prior run (fast
    # iteration on the Phase B path): skip the build/wipe/install entirely.
    if phase_b_only:
        if not os.path.isfile(os.path.join(rundir, "disk1.qcow2")):
            log(f"--phase-b-only: no installed disks in {rundir}; run a full pass first")
            return False
        log(f"=== run {name} (phase-b-only) -> {rundir} ===")
        return _phase_b(d, rundir, mem, smp, serial_log, keep)

    shutil.rmtree(rundir, ignore_errors=True)
    os.makedirs(rundir, exist_ok=True)
    log(f"=== run {name} -> {rundir} ===")

    # Build the binary into the run dir (shared into the VM; also what stageBinary
    # copies into the target home).
    subprocess.run(["go", "build", "-o", os.path.join(rundir, "archwright"), "."],
                   cwd=REPO, check=True)
    shutil.copy(os.path.join(HERE, d["config"]), os.path.join(rundir, "config.yaml"))
    write_scaffold(rundir, d)
    shutil.copy(OVMF_VARS, os.path.join(rundir, "OVMF_VARS.fd"))

    # Fresh disks.
    for i, size in enumerate(d["disks"]):
        subprocess.run(["qemu-img", "create", "-f", "qcow2",
                        os.path.join(rundir, f"disk{i+1}.qcow2"), size],
                       check=True, stdout=subprocess.DEVNULL)

    # ---- Phase A: ISO boot, install, inject scaffold --------------------
    kern, init, label = ensure_kernel()
    args = qemu_common(rundir, d["disks"], mem, smp) + [
        "-cdrom", iso_path(),
        "-kernel", kern, "-initrd", init,
        "-append",
        f"archisobasedir=arch archisolabel={label} cow_spacesize=2G rw "
        "console=ttyS0,115200 "
        "systemd.mount-extra=e2e:/root/e2e:9p:trans=virtio,version=9p2000.L,ro",
        "-virtfs",
        f"local,path={rundir},mount_tag=e2e,security_model=none,readonly=on",
    ]
    proc = boot(args, rundir, "ISO / Phase A")
    con = Serial(os.path.join(rundir, "serial.sock"), serial_log)
    ok = False
    iso_result = None  # set when the descriptor validates on the ISO (encrypted layouts)
    try:
        con.wait_ready(timeout=420)
        log("live ISO shell ready; staging binary + config")
        con.run_ok("cp /root/e2e/archwright /root/archwright && chmod +x /root/archwright", timeout=30)
        con.run_ok("cp /root/e2e/config.yaml /root/config.yaml", timeout=30)
        log("running archwright install --yes (this pacstraps; minutes)")
        rc = con.run("/root/archwright install --yes --config /root/config.yaml", timeout=2400)
        if rc != 0:
            log(f"PHASE A FAILED: archwright install rc={rc} (see {serial_log})")
            return False
        log("Phase A install complete")
        # Encrypted layouts skip Phase B staging (postInstall bails on a LUKS
        # remount we haven't implemented), so they validate the on-disk LUKS
        # layout here on the live ISO instead of booting Phase B.
        if d.get("iso_validate"):
            iso_result = run_iso_validate(con, d)
        elif d.get("phase_b", True):
            inject_phase_b(con, d)
        con.run("systemctl poweroff", timeout=10)
        ok = True
    except (TimeoutError, EOFError, RuntimeError) as e:
        log(f"PHASE A ERROR: {e} (see {serial_log})")
    finally:
        poweroff_and_wait(con, proc, timeout=120)

    if d.get("iso_validate"):
        log(f"ISO-validate result: {'PASS' if (ok and iso_result) else 'FAIL'}")
        return bool(ok and iso_result)
    if not ok or not d.get("phase_b", True):
        return ok

    return _phase_b(d, rundir, mem, smp, serial_log, keep)


def run_iso_validate(con: Serial, d: dict) -> bool:
    """Run the descriptor's on-ISO assertions (used for encrypted layouts, which
    can't run Phase B). Each command must exit 0; the throwaway install password
    is 'installme' (what --yes feeds archinstall), so a LUKS open uses it."""
    log("running on-ISO layout validation (encrypted)")
    allok = True
    for cmd in d["iso_validate"]:
        rc = con.run(cmd, timeout=120)
        log(f"  [{'OK' if rc == 0 else 'FAIL'}] {cmd}")
        if rc != 0:
            allok = False
    return allok


def _phase_b(d: dict, rundir: str, mem: str, smp: str, serial_log: str, keep: bool) -> bool:
    """Disk boot: the injected scaffold autologins, runs bootstrap + validate, and
    powers off. We just watch the serial for the PASS/FAIL marker."""
    args = qemu_common(rundir, d["disks"], mem, smp)  # no cdrom/kernel/9p; boot from disk
    proc = boot(args, rundir, "disk / Phase B")
    con = Serial(os.path.join(rundir, "serial.sock"), serial_log)
    result = False
    try:
        log("waiting for Phase B (bootstrap + validate; minutes)")
        m = con.expect(r"E2E_RESULT=(PASS|FAIL)", timeout=2700)
        result = m.group(1) == "PASS"
        try:
            con.expect(r"E2E_PHASEB_DONE", timeout=120)
        except (TimeoutError, EOFError):
            pass
        log(f"Phase B result: {'PASS' if result else 'FAIL'}")
    except (TimeoutError, EOFError) as e:
        log(f"PHASE B ERROR: {e} (see {serial_log})")
    finally:
        poweroff_and_wait(con, proc, timeout=120)

    if not keep and result:
        # Reclaim the big qcow2 files on success; keep logs.
        for i in range(len(d["disks"])):
            try:
                os.unlink(os.path.join(rundir, f"disk{i+1}.qcow2"))
            except FileNotFoundError:
                pass
    return result


def main() -> int:
    ap = argparse.ArgumentParser(description="archwright VM e2e harness")
    ap.add_argument("names", nargs="*", help="matrix descriptor names to run (default: all)")
    ap.add_argument("--list", action="store_true", help="list descriptors and exit")
    ap.add_argument("--keep", action="store_true", help="keep qcow2 disks after a passing run")
    ap.add_argument("--phase-b-only", action="store_true",
                    help="skip install; re-boot existing installed disks and re-run Phase B")
    ap.add_argument("-j", "--jobs", type=int, default=1,
                    help="run up to N descriptors concurrently (each VM ~4G/4vcpu); default 1")
    args = ap.parse_args()

    if args.list:
        for d in MATRIX:
            print(d["name"])
        return 0

    selected = MATRIX if not args.names else [d for d in MATRIX if d["name"] in args.names]
    if not selected:
        sys.exit(f"no matching descriptors: {args.names} (have: {[d['name'] for d in MATRIX]})")

    os.makedirs(WORK, exist_ok=True)
    jobs = max(1, min(args.jobs, len(selected)))

    def run_one(d: dict) -> bool:
        try:
            return run_descriptor(d, args.keep, args.phase_b_only)
        except Exception as e:  # noqa: BLE001 — report and continue the matrix
            log(f"run {d['name']} crashed: {e}")
            return False

    results: dict[str, bool] = {}
    if jobs == 1:
        for d in selected:
            results[d["name"]] = run_one(d)
    else:
        # Each run is fully isolated (own rundir/disks/serial socket/NVRAM), so a
        # thread per descriptor is safe; threads idle on the qemu/serial I/O. Warm
        # the shared kernel cache once up front to avoid a startup stampede.
        log(f"running {len(selected)} descriptor(s) with up to {jobs} concurrent VM(s)")
        ensure_kernel()
        with concurrent.futures.ThreadPoolExecutor(max_workers=jobs) as pool:
            futs = {pool.submit(run_one, d): d["name"] for d in selected}
            for fut in concurrent.futures.as_completed(futs):
                results[futs[fut]] = fut.result()

    print("\n==== e2e summary ====")
    for name in sorted(results):
        print(f"  {'PASS' if results[name] else 'FAIL'}  {name}")
    return 0 if all(results.values()) else 1


if __name__ == "__main__":
    sys.exit(main())
