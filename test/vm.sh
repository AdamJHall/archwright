#!/usr/bin/env bash
# Local QEMU smoke-test harness for the full archwright flow.
#
# Builds the binary, (re)creates three virtual disks, and boots a UEFI VM with
# the project directory shared in over virtio-9p — so the freshly-built binary
# and config.yaml show up inside the live ISO with no scp / ISO rebuild. Rebuild
# on the host, re-run the binary in the VM.
#
#   ./test/vm.sh iso          # boot the Arch live ISO (run Phase A here)
#   ./test/vm.sh iso --fresh  # wipe the qcow2 disks first (clean install)
#   ./test/vm.sh disk         # boot the *installed* system off disk 1
#
# Disk sizes are env-overridable (qcow2 is thin, so these are just geometry):
#   DISK1=40G DISK2=16G DISK3=16G ./test/vm.sh iso
#
# The repo share auto-mounts at /mnt/host (read-only). Inside the live ISO:
#   cp /mnt/host/archwright /root/ && cp /mnt/host/config.yaml /root/   # or write one
#   /root/archwright install --dry-run        # inspect rendered archinstall JSON
#   /root/archwright install --yes            # destructive: erases vda/vdb/vdc
# (NOAUTO=1 ./test/vm.sh iso falls back to the plain menu boot; then mount by hand:
#   mkdir -p /mnt/host && mount -t 9p -o trans=virtio,version=9p2000.L host /mnt/host)
set -euo pipefail

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORK="${VM_WORK:-$REPO/.vm}"
ISO="${ARCH_ISO:-$REPO/.iso/archlinux-2026.06.01-x86_64.iso}"
OVMF_CODE="/usr/share/edk2/x64/OVMF_CODE.4m.fd"
OVMF_VARS_SRC="/usr/share/edk2/x64/OVMF_VARS.4m.fd"

# Disk 1 holds ESP + swap + root LV; 20G fits a base + KDE install. Disks 2/3
# are whole-disk PVs that join the VG — small is fine for a layout smoke test.
DISK1="${DISK1:-20G}"; DISK2="${DISK2:-8G}"; DISK3="${DISK3:-8G}"

mode="${1:-iso}"; shift || true
fresh=0; for a in "$@"; do [[ "$a" == "--fresh" ]] && fresh=1; done

mkdir -p "$WORK"

# 1. Build the static binary into the repo so the 9p share picks it up.
echo ">> building archwright"
( cd "$REPO" && go build -o archwright . )

# 2. Disks -> vda/vdb/vdc inside the VM.
[[ $fresh == 1 ]] && rm -f "$WORK"/disk{1,2,3}.qcow2
[[ -f "$WORK/disk1.qcow2" ]] || qemu-img create -f qcow2 "$WORK/disk1.qcow2" "$DISK1"
[[ -f "$WORK/disk2.qcow2" ]] || qemu-img create -f qcow2 "$WORK/disk2.qcow2" "$DISK2"
[[ -f "$WORK/disk3.qcow2" ]] || qemu-img create -f qcow2 "$WORK/disk3.qcow2" "$DISK3"

# 3. Per-VM writable UEFI NVRAM copy, so the installed system's boot entry
#    persists across reboots (needed to test the `disk` mode below).
[[ -f "$WORK/OVMF_VARS.fd" ]] || cp "$OVMF_VARS_SRC" "$WORK/OVMF_VARS.fd"

common=(
  # -cpu host passes the host's full feature set through (x86-64-v3/v4). Without
  # it QEMU defaults to the baseline qemu64 model (v1), and CachyOS's repo setup
  # detects an unsupported CPU and skips adding its repos -> `target not found:
  # linux-cachyos` at kernel install.
  -enable-kvm -cpu host -m 8G -smp 4 -machine q35
  -drive "if=pflash,format=raw,readonly=on,file=$OVMF_CODE"
  -drive "if=pflash,format=raw,file=$WORK/OVMF_VARS.fd"
  -drive "file=$WORK/disk1.qcow2,if=virtio"
  -drive "file=$WORK/disk2.qcow2,if=virtio"
  -drive "file=$WORK/disk3.qcow2,if=virtio"
  # Share the repo in read-only over 9p (tag "host"). Holds archwright + config.
  -virtfs "local,path=$REPO,mount_tag=host,security_model=none,readonly=on"
)

case "$mode" in
  iso)
    [[ -f "$ISO" ]] || { echo "ISO not found: $ISO (set ARCH_ISO=...)" >&2; exit 1; }
    if [[ "${NOAUTO:-0}" == 1 ]]; then
      echo ">> booting live ISO (manual mount; NOAUTO=1). Disks: $WORK/disk{1,2,3}.qcow2"
      exec qemu-system-x86_64 "${common[@]}" -cdrom "$ISO" -boot menu=on
    fi
    # Direct-boot the ISO's own kernel so we can pass a cmdline that auto-mounts
    # the 9p share at /mnt/host (systemd.mount-extra). Kernel + initramfs are
    # extracted from the ISO (cached, refreshed when the ISO changes) with bsdtar
    # — no root needed. archisolabel must match the ISO volume label so the
    # archiso initramfs finds the squashfs on the -cdrom device.
    if [[ ! -f "$WORK/vmlinuz-linux" || "$ISO" -nt "$WORK/vmlinuz-linux" ]]; then
      echo ">> extracting kernel/initramfs from ISO"
      bsdtar -xf "$ISO" -C "$WORK" \
        arch/boot/x86_64/vmlinuz-linux arch/boot/x86_64/initramfs-linux.img
      mv "$WORK"/arch/boot/x86_64/{vmlinuz-linux,initramfs-linux.img} "$WORK/"
      rm -rf "$WORK/arch"
    fi
    label="$(blkid -p -s LABEL -o value "$ISO")"
    echo ">> booting live ISO; share auto-mounts at /mnt/host. Disks: $WORK/disk{1,2,3}.qcow2"
    exec qemu-system-x86_64 "${common[@]}" -cdrom "$ISO" \
      -kernel "$WORK/vmlinuz-linux" -initrd "$WORK/initramfs-linux.img" \
      -append "archisobasedir=arch archisolabel=$label rw systemd.mount-extra=host:/mnt/host:9p:trans=virtio,version=9p2000.L" ;;
  disk)
    echo ">> booting installed system off disk 1"
    exec qemu-system-x86_64 "${common[@]}" -boot c ;;
  *)
    echo "usage: $0 {iso|disk} [--fresh]" >&2; exit 1 ;;
esac
