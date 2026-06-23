# Automated VM end-to-end harness

`e2e.py` drives a **fully automated** QEMU run of the complete archwright flow —
boot the live ISO, run Phase A (`install --yes`), reboot from disk, run Phase B
(`bootstrap`) and assert the installed system — with **no human interaction**.
It is the last-mile validation the render tests and the loopback `disks.sh`
harness can't give (see `docs/vm-validation.md`): proof that a real archinstall
*does the right thing end-to-end on a booted system* for each layout/feature.

## Two test groups

- **Disk-layout matrix** (`matrix/lvm.py`, `btrfs.py`, `plain.py`, `lvm_variants.py`,
  `systemd_boot.py`, `encryption.py`) — every partitioning/swap/bootloader/encryption
  combination, validated by `lib/validate.sh`.
- **Feature / stage coverage** (`matrix/features.py`, `features_extra.py`) — exercises
  the Phase A/B *stages* (reflector, custom kernel, plymouth, hooks, setup, services,
  dotfiles, flatpak, repos, KDE) on a fixed minimal layout, validated by
  `lib/features.sh`. `features-min` bundles all the cheap features into one VM;
  the heavier ones (flatpak runtime, KDE desktop, dotfiles, a custom repo) are
  separate so they can be run on demand.

## Running

```sh
task vm-e2e -- lvm-multi          # one descriptor
task vm-e2e -- features-min       # the cheap feature bundle
task vm-e2e                       # the whole matrix (sequential)
task vm-e2e -- --jobs 5           # whole matrix, up to 5 VMs at once
task vm-e2e -- -j 4 lvm-multi btrfs-basic plain-ext4 features-min   # a subset, 4 at a time
task vm-e2e-list                  # list descriptors
python3 test/e2e/vm/e2e.py lvm-multi --phase-b-only   # re-run Phase B on existing disks
```

`--jobs/-j N` runs up to N descriptors concurrently. Each VM is fully isolated (its own
run dir, disks, serial socket, NVRAM) and uses ~4 GiB RAM + 4 vcpus, so size N to the host
(e.g. 4–5 on a 16-core/32 GiB box). Concurrent log lines are tagged `[e2e][<name>]`; each
run still writes its own `.e2e/runs/<name>/serial.log`.

Everything lands under the gitignored `.e2e/` (per-run disks, NVRAM, and a `serial.log` you
can read after a failure).

## Requirements

The harness shells out to QEMU/OVMF and a few CLI tools; **the Python driver itself is pure
stdlib (no `pip` packages)**. On Arch:

| Need | Provides | Arch package | Notes |
|------|----------|--------------|-------|
| `qemu-system-x86_64`, `qemu-img` | the VM + disk images | `qemu-base` (or `qemu-system-x86` + `qemu-img`; `qemu-full` also works) | |
| OVMF firmware (`/usr/share/edk2/x64/OVMF_CODE.4m.fd`, `OVMF_VARS.4m.fd`) | UEFI boot | `edk2-ovmf` | path is hard-coded in `e2e.py` |
| `/dev/kvm` | hardware acceleration | kernel KVM + your user in the **`kvm`** group | enable virtualization (VT-x/AMD-V) in firmware; `-cpu host` is used |
| `bsdtar` | extract kernel/initramfs from the ISO | `libarchive` | |
| `blkid` | read the ISO volume label | `util-linux` | part of `base` |
| `git` | builds the injected dotfiles repo (`inject_repo`) | `git` | also needed by archwright itself |
| `curl` | download the pinned ISO (`task iso`) | `curl` | |
| `python3` ≥ 3.9 | the orchestrator | `python` | stdlib only |
| `go`, `task` | build `archwright`, run the tasks | provisioned by **`mise install`** (`mise.toml` pins Go + Task) | the rest of the repo's toolchain |

One-time setup on a fresh Arch box (besides `mise install` for Go/Task):

```sh
sudo pacman -S --needed qemu-base edk2-ovmf libarchive git curl
sudo usermod -aG kvm "$USER"   # then re-login so /dev/kvm is usable
task iso                       # cache the pinned Arch ISO under .iso/
```

## How it works

1. **Phase A** — boots the ISO headless with the serial console on a unix socket
   (`console=ttyS0`, archiso autologins root). The per-run dir is shared in over
   9p at `/root/e2e`. The driver copies the freshly built binary + config in and
   runs `archwright install --yes`. `--yes` makes Phase A non-interactive (it
   uses a throwaway password and skips the erase prompt).
2. **Scaffold injection** — still on the live ISO, with the freshly installed
   target remounted at `/mnt` (per the descriptor's `root_mount`), the driver
   injects a **harness-only** Phase-B autorun into the installed system: a
   NOPASSWD sudoers drop-in, a `serial-getty@ttyS0` autologin as the user, a
   `~/.bash_profile` trigger, and the `e2e-bootstrap.sh` + `validate.sh` scripts.
   **None of this lives in any real config** — it is pure test scaffolding.
3. **Phase B** — reboots from disk. The autologin lands a real user session on
   the serial console, `~/.bash_profile` runs `archwright bootstrap` then
   `validate.sh`, prints `E2E_RESULT=PASS|FAIL`, and powers off. The driver
   watches the serial for that marker.

## Adding coverage (the descriptor contract)

Grow the matrix by adding a **new file** `matrix/<family>.py` exporting a
`DESCRIPTORS` list, plus a `configs/<name>.yaml`. Don't edit `e2e.py`,
`lib/validate.sh`, or another family's files — new-files-only keeps everything
conflict-free. A descriptor:

```python
DESCRIPTORS = [{
    "name": "plain-ext4",                 # unique; also the run-dir/log name
    "config": "configs/plain-ext4.yaml",  # path relative to e2e.py
    "disks": ["12G"],                     # qcow2 sizes -> vda, vdb, vdc ...
    "user": "e2e",                        # config's user.name; MUST be a bash login shell
    "phase_b": True,                      # run Phase B? (False = install-only)
    "esp_part": "/dev/vda1",              # ESP partition; the orchestrator mounts it
    "root_mount": [                       # at /mnt/boot itself (with mkdir -p)
        "mount /dev/vda2 /mnt",           # AS ROOT on the ISO, post-install: mount ONLY
    ],                                    # the installed root at /mnt (NOT /mnt/boot)
    "grub_serial": True,                  # append console=ttyS0 to installed GRUB (grub only)
    "expect": {                           # EXPECT_* fed to validate.sh
        "LAYOUT": "plain", "ROOT_FS": "ext4", "SWAP": "swapfile",
        "BOOTLOADER": "grub", "HOSTNAME": "arch-e2e", "USER": "e2e",
        "PACKAGES": "tree jq", "AUR_HELPER": "yay", "ENCRYPTION": "0",
    },
}]
```

### Device + mount-recipe rules (must match how archwright partitions)

The config's disk devices are the VM's virtio names: disk 1 = `/dev/vda`,
disk 2 = `/dev/vdb`, disk 3 = `/dev/vdc`. Disk 1 is always `ESP (p1) + …`.
`root_mount` mounts **only the installed root** at `/mnt`; the orchestrator then
mounts `esp_part` at `/mnt/boot` itself (creating the mountpoint), so don't add a
`/mnt/boot` line.

- **lvm**: PVs are `vda2` + any whole extra disks; root is `/dev/<vg>/<lv>`.
  Recipe: `vgchange -ay <vg>` · `mount /dev/<vg>/<lv> /mnt`.
  (Multi-volume: `<lv>` is the volume whose mountpoint is `/`.)
- **plain**: root is `vda2` (or `vda3` when `swap.type: partition`, since swap is
  `p2`). Recipe: `mount /dev/vda2 /mnt`.
- **btrfs**: root is `vda2` (or `vda3` with a swap partition). Mount the bare
  partition (`mount /dev/vda2 /mnt`) — see the finding below; archinstall installs
  to the top-level subvolume, which is the partition's default mount.

### Finding: btrfs installs to the top-level subvolume

A diagnostic run showed that with archwright's btrfs render, archinstall installs
the whole system (and the user home) into the **top-level** btrfs subvolume
(`subvolid 5`, the default). The subvolumes named in `disks.btrfs.subvolumes`
(e.g. `@`) are created but **not** used as the root mount — `@` is left empty. The
install is self-consistent (archwright's `postInstall`/`rootDevice` also use the
bare partition, so staging + boot agree), which is why the e2e btrfs recipe mounts
the bare partition rather than `subvol=@`. But the conventional `@`-rooted,
snapshot-friendly layout the config implies is **not** what gets built — this is a
real follow-up for the reverse-engineered btrfs subvolume JSON shape (the
VM-validation-pending item in `CLAUDE.md`). The btrfs e2e configs therefore use a
single `@` entry and don't rely on a separate `@home`.

### Keep Phase B cheap + deterministic

Trim configs like `configs/lvm-multi.yaml`: a bash login shell, `reflector:
false`, a couple of tiny official packages, `desktop.environment: none`,
`dotfiles.manager: none`, no flatpaks/AUR/custom-kernels/heavy theming. The point
is to exercise each stage's *wiring*, not to download a desktop. `validate.sh`
already covers lvm/btrfs/plain, every swap type, and grub/systemd-boot, gated on
`EXPECT_LAYOUT` — extend it only for a genuinely new assertion.

### Validate without booting a VM

```sh
go build -o archwright .
./archwright validate --config test/e2e/vm/configs/<name>.yaml
python3 test/e2e/vm/e2e.py --list        # your descriptor should appear
```
