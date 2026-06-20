#!/usr/bin/env bash
#
# disks.sh - Tier 2 integration harness for archwright's Phase A.
#
# WHAT THIS PROVES
#   archwright's job in Phase A is to RENDER config.yaml into an archinstall JSON
#   config + creds file and hand them to the official `archinstall` tool. The one
#   thing unit tests cannot prove is the #1 gotcha in CLAUDE.md: that our
#   reverse-engineered archinstall JSON is actually ACCEPTED by a real archinstall
#   and describes a coherent disk layout against real block devices.
#
#   This harness creates loopback block devices, renders archwright's config
#   against them, then feeds the rendered JSON to archinstall to validate (and
#   optionally fully install) it.
#
# TWO MODES (--mode)
#   light  (default)  archinstall --config ... --creds ... --silent --dry-run
#                     archinstall parses + validates the config and builds its
#                     device model against the live loop devices WITHOUT
#                     partitioning, formatting or pacstrapping. No network, fast,
#                     safe to run on every PR. This is the gate that catches a
#                     schema drift after an archinstall version bump.
#   full              A real archinstall run that partitions the loop devices,
#                     creates the LVM stack, formats and pacstraps. Slow, needs
#                     network + disk space; gated behind workflow_dispatch /
#                     nightly in CI. Afterwards we assert the on-disk layout with
#                     lsblk/blkid/findmnt/vgs/lvs.
#
# WHY loop devices work here
#   archinstall enumerates loop devices: it shells out to `losetup` to list
#   active loops and resolves each via parted's getDevice(). So loops attached
#   with `losetup -fP` (partition scanning on) appear in its device model
#   exactly like real disks. `-P` is required so the pN partition nodes (e.g.
#   /dev/loop0p2, our disk-1 LVM PV) materialise.
#
# WHAT THIS HARNESS DOES *NOT* do to production code
#   It extracts the rendered archinstall JSON from `archwright install --dry-run`
#   stderr (a pristine, contiguous JSON block printed verbatim by the stage). No
#   production Go change is required: dry-run already prints the config, and with
#   real attached loop devices `blockdev --getsize64` returns true sizes, so the
#   rendered byte layout is the real one (not the 512 GiB dry-run placeholder).
#
# REQUIREMENTS (root): losetup, lvm2, archinstall, dosfstools, e2fsprogs,
#   xfsprogs, util-linux, go (to build the binary).
#
set -euo pipefail

# --- parameters -------------------------------------------------------------
# Defaults describe the canonical 3-disk layout from config.example.yaml:
# disk1 = ESP + swap + remainder-as-PV ; disk2/disk3 = whole-disk PVs.
MODE="light"            # light | full
LAYOUT="multi-disk-lvm" # single-disk-lvm | multi-disk-lvm
FS="xfs"                # root filesystem: xfs | ext4
DISK1_SIZE="6G"         # disk 1 backing file size
EXTRA_SIZE="4G"         # each extra disk backing file size
ESP_SIZE="512MiB"
SWAP_SIZE="1GiB"
VG="vg0"
LV="root"
WORKDIR="${WORKDIR:-$(mktemp -d /tmp/archwright-e2e.XXXXXX)}"

usage() {
	cat <<-USAGE
	usage: $0 [--mode light|full] [--layout single-disk-lvm|multi-disk-lvm]
	          [--fs xfs|ext4] [--disk1-size SZ] [--extra-size SZ]

	  --mode    light = archinstall --dry-run validation only (default)
	            full  = real partition/format/pacstrap, then assert layout
	  --layout  single-disk-lvm = one disk (ESP+swap+PV)
	            multi-disk-lvm  = three disks (default; matches config.example)
	  --fs      root LV filesystem (default: xfs)
	USAGE
}

while [[ $# -gt 0 ]]; do
	case "$1" in
	--mode) MODE="$2"; shift 2 ;;
	--layout) LAYOUT="$2"; shift 2 ;;
	--fs) FS="$2"; shift 2 ;;
	--disk1-size) DISK1_SIZE="$2"; shift 2 ;;
	--extra-size) EXTRA_SIZE="$2"; shift 2 ;;
	-h | --help) usage; exit 0 ;;
	*) echo "unknown arg: $1" >&2; usage; exit 2 ;;
	esac
done

case "$MODE" in light | full) ;; *) echo "bad --mode: $MODE" >&2; exit 2 ;; esac
case "$FS" in xfs | ext4) ;; *) echo "bad --fs: $FS" >&2; exit 2 ;; esac
case "$LAYOUT" in single-disk-lvm | multi-disk-lvm) ;; *) echo "bad --layout: $LAYOUT" >&2; exit 2 ;; esac

if [[ $EUID -ne 0 ]]; then
	echo "This harness must run as root (losetup / lvm / archinstall)." >&2
	exit 1
fi

# --- locate the repo + build the binary -------------------------------------
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
BIN="$WORKDIR/archwright"

log() { printf '\n=== %s ===\n' "$*"; }

# --- cleanup (always) -------------------------------------------------------
# Tracked so the trap only tears down what we created, in the right order.
LOOPS=()
cleanup() {
	local rc=$?
	set +e
	# LVM first: deactivate the VG so its PVs (our loops) can detach.
	if vgs --noheadings -o vg_name 2>/dev/null | tr -d ' ' | grep -qx "$VG"; then
		vgchange -an "$VG" >/dev/null 2>&1
		vgremove -f "$VG" >/dev/null 2>&1
	fi
	# Unmount anything we mounted under the workdir target.
	if mountpoint -q "$WORKDIR/target/boot" 2>/dev/null; then umount "$WORKDIR/target/boot"; fi
	if mountpoint -q "$WORKDIR/target" 2>/dev/null; then umount "$WORKDIR/target"; fi
	# Detach loop devices.
	local d
	for d in "${LOOPS[@]:-}"; do
		[[ -n "$d" ]] && losetup -d "$d" >/dev/null 2>&1
	done
	# Drop the scratch dir (backing files live here).
	rm -rf "$WORKDIR"
	exit $rc
}
trap cleanup EXIT INT TERM

# --- create + attach backing files -----------------------------------------
log "creating loop devices in $WORKDIR (mode=$MODE layout=$LAYOUT fs=$FS)"
mkdir -p "$WORKDIR"

attach() { # attach <backing-file> <size> -> prints loop device path
	local f="$1" sz="$2" dev
	truncate -s "$sz" "$f"
	dev="$(losetup -fP --show "$f")" # -P: scan partition table so loopNpX appear
	echo "$dev"
}

DISK1="$(attach "$WORKDIR/disk1.img" "$DISK1_SIZE")"
LOOPS+=("$DISK1")
echo "disk1 -> $DISK1"

EXTRA_PVS=()
if [[ "$LAYOUT" == "multi-disk-lvm" ]]; then
	for i in 2 3; do
		dev="$(attach "$WORKDIR/disk$i.img" "$EXTRA_SIZE")"
		LOOPS+=("$dev")
		EXTRA_PVS+=("$dev")
		echo "disk$i -> $dev"
	done
fi

# The disk-1 LVM PV is partition 2 of disk1 (ESP is p1; there is no swap
# partition -- swap is a post-install /swapfile). losetup uses the pN suffix
# (/dev/loop0 -> /dev/loop0p2), matching archinstall's partition ordering.
DISK1_PV="${DISK1}p2"

# --- render an archwright config.yaml at those devices ----------------------
# Self-contained: does NOT depend on internal/archinstall/testdata.
CONFIG="$WORKDIR/config.yaml"
{
	cat <<-YAML
	system:
	  hostname: arch-e2e
	  timezone: Europe/London
	  locale: en_GB.UTF-8
	  keymap: uk
	user:
	  name: e2e
	  shell: /usr/bin/bash
	  groups: [wheel]
	disks:
	  esp:
	    device: $DISK1
	    size: $ESP_SIZE
	  swap:
	    size: $SWAP_SIZE
	  lvm:
	    vg: $VG
	    lv: $LV
	    filesystem: $FS
	    pvs:
	      - $DISK1_PV
	YAML
	for pv in "${EXTRA_PVS[@]:-}"; do
		[[ -n "$pv" ]] && echo "      - $pv"
	done
	cat <<-YAML
	mirrors:
	  reflector: false
	packages: []
	YAML
} >"$CONFIG"

log "rendered config.yaml"
cat "$CONFIG"

# --- build archwright + validate the config ---------------------------------
log "building archwright"
( cd "$REPO_ROOT" && go build -o "$BIN" . )

log "archwright validate"
"$BIN" validate --config "$CONFIG"

# --- render the archinstall JSON --------------------------------------------
# `install --dry-run` prints the rendered archinstall config as a pristine,
# contiguous JSON block on stderr (fmt.Fprintln of json.MarshalIndent, top-level
# braces at column 0). The surrounding charmbracelet log lines never start with a
# bare "{"/"}" line, so this awk window extracts exactly the JSON object. With
# real loop devices attached, blockdev returns true sizes -> real byte layout.
AI_CONFIG="$WORKDIR/archinstall-config.json"
log "rendering archinstall config via archwright install --dry-run"
"$BIN" install --only archinstall --yes --dry-run --config "$CONFIG" 2>"$WORKDIR/dryrun.stderr" || {
	echo "archwright install --dry-run failed; stderr:" >&2
	cat "$WORKDIR/dryrun.stderr" >&2
	exit 1
}
awk '/^\{$/{f=1} f{print} /^\}$/{if(f){print "";exit}}' \
	"$WORKDIR/dryrun.stderr" >"$AI_CONFIG"

if ! grep -q '"disk_config"' "$AI_CONFIG"; then
	echo "failed to extract archinstall JSON from dry-run output" >&2
	echo "--- captured ---" >&2; cat "$AI_CONFIG" >&2
	echo "--- raw stderr ---" >&2; cat "$WORKDIR/dryrun.stderr" >&2
	exit 1
fi

# Sanity: it must be valid JSON and reference our devices.
python3 -c "import json,sys; json.load(open('$AI_CONFIG'))"
grep -q "$DISK1" "$AI_CONFIG" || { echo "rendered JSON does not mention $DISK1" >&2; exit 1; }
log "extracted + parsed archinstall config:"
cat "$AI_CONFIG"

# archinstall wants a creds file too. dry-run withholds creds (secrets), so write
# a throwaway one matching the schema (internal/archinstall.Creds) for the loop.
AI_CREDS="$WORKDIR/archinstall-creds.json"
cat >"$AI_CREDS" <<'CREDS'
{
  "users": [{"username": "e2e", "!password": "installme", "sudo": true}],
  "!root-password": "installme"
}
CREDS

# --- LIGHT: validate config against a real archinstall (no install) ---------
if [[ "$MODE" == "light" ]]; then
	log "archinstall --dry-run (validate our JSON against real archinstall $(archinstall --version 2>/dev/null || echo '?'))"
	# --dry-run: archinstall parses the config + builds its device model against
	# the live loop devices but takes no permanent action. Non-zero exit = our
	# reverse-engineered schema was rejected (the failure we want to catch).
	archinstall --config "$AI_CONFIG" --creds "$AI_CREDS" --silent --dry-run
	log "LIGHT validation PASSED: archinstall accepted the rendered config"
	exit 0
fi

# --- FULL: real install, then assert the on-disk layout ---------------------
log "archinstall --silent (FULL install onto loop devices)"
archinstall --config "$AI_CONFIG" --creds "$AI_CREDS" --silent

log "asserting resulting layout"

fail() { echo "ASSERT FAILED: $*" >&2; exit 1; }

# ESP must be fat32 on disk1 partition 1.
ESP_PART="${DISK1}p1"
esp_fs="$(blkid -s TYPE -o value "$ESP_PART" 2>/dev/null || true)"
[[ "$esp_fs" == "vfat" ]] || fail "ESP $ESP_PART expected vfat (fat32), got '$esp_fs'"
echo "OK: ESP $ESP_PART is fat32 (vfat)"

# There is no swap partition: swap is a /swapfile created by archwright's
# post-install step (this harness runs archinstall directly, so it only asserts
# the archinstall-produced layout). Disk1 partition 2 is the LVM PV.

# Volume group must exist and carry the expected number of PVs.
vgs --noheadings -o vg_name | tr -d ' ' | grep -qx "$VG" || fail "VG '$VG' not found"
echo "OK: VG '$VG' exists"

expected_pvs=1
[[ "$LAYOUT" == "multi-disk-lvm" ]] && expected_pvs=3
pv_count="$(vgs --noheadings -o pv_count "$VG" | tr -d ' ')"
[[ "$pv_count" == "$expected_pvs" ]] || fail "VG '$VG' expected $expected_pvs PVs, got $pv_count"
echo "OK: VG '$VG' has $pv_count PV(s)"

# Root LV must exist with the requested filesystem.
lvs --noheadings -o lv_name "$VG" | tr -d ' ' | grep -qx "$LV" || fail "LV '$LV' not found in '$VG'"
LV_DEV="/dev/$VG/$LV"
lv_fs="$(blkid -s TYPE -o value "$LV_DEV" 2>/dev/null || true)"
[[ "$lv_fs" == "$FS" ]] || fail "root LV $LV_DEV expected fs '$FS', got '$lv_fs'"
echo "OK: root LV $LV_DEV is $FS"

# And the layout should be mountable: root LV at /, ESP under /boot.
mkdir -p "$WORKDIR/target"
mount "$LV_DEV" "$WORKDIR/target"
findmnt -n "$WORKDIR/target" >/dev/null || fail "root LV did not mount"
mkdir -p "$WORKDIR/target/boot"
mount "$ESP_PART" "$WORKDIR/target/boot"
findmnt -n "$WORKDIR/target/boot" >/dev/null || fail "ESP did not mount under /boot"
echo "OK: root + ESP mount cleanly"

log "FULL run PASSED: layout matches config"
lsblk "$DISK1" "${EXTRA_PVS[@]:-}"
