# Bug: btrfs install lands on the top-level subvolume; configured subvolumes (`@`) are unused as root

**Status:** open — found by the automated VM e2e harness (`test/e2e/vm/`), 2026-06-23
**Area:** `internal/archinstall/archinstall.go` (`btrfsBuilder` / `singleDiskRoot`) — the
reverse-engineered btrfs subvolume JSON shape (the VM-validation-pending item in `CLAUDE.md`)
**Severity:** medium — installs boot and are self-consistent, but the intended `@`-rooted,
snapshot-friendly btrfs layout is **not** what gets built, so snapper/rollback workflows that
assume a `@` root subvolume will not behave as expected.

## Summary

For the `btrfs` disk layout, archwright renders the root partition with **both** a
partition-level `mountpoint: "/"` **and** a subvolume `@` whose `mountpoint` is also `"/"`.
A real archinstall 4.3 run resolves that by mounting the **top-level** btrfs subvolume
(subvolid 5) at `/` and installing the whole system there. The configured `@` subvolume is
created but left **empty** and is never used as the root. The conventional Arch btrfs layout
(system installed *inside* `@`, mounted with `subvol=@`) is therefore not produced.

archwright itself is internally consistent with this — its post-install chroot work
(`rootDevice()` → `PartDev(esp, 2)`, then `mount <part> /mnt`) mounts the bare partition,
i.e. the default/top-level subvolume — so staging and boot agree and the machine boots fine.
The defect is purely that the **named subvolumes are not honored as the root**.

## Evidence

### Rendered JSON (the root partition for `disks.layout: btrfs`)

`./archwright install --only archinstall --yes --dry-run --config <btrfs config>` emits, for
the root partition:

```json
{
  "fs_type": "btrfs",
  "mountpoint": "/",                              // <-- partition mounted at / (top-level subvol)
  "mount_options": ["compress=zstd"],
  "btrfs": [
    { "name": "@", "mountpoint": "/" }            // <-- @ ALSO claims / ; created but unused
  ]
}
```

The conflict is the partition having `mountpoint: "/"` while a subvolume also maps to `/`.

### Observed on a real VM (diagnostic from the e2e harness)

After a successful `archwright install` of a btrfs config, on the live ISO:

```
# btrfs subvolume get-default /mnt      ->  ID 5 (FS_TREE)          # default = top-level, not @
# mount -o subvol=@ /dev/vda2 /mnt ; ls /mnt/home   ->  (empty)     # @ is empty
# mount -o subvolid=5 /dev/vda2 /mnt-top ; ls /mnt-top
    bin boot dev etc home lib ... usr var @        # the whole system is in the TOP-LEVEL subvol,
                                                    # with @ present only as an empty subdir/subvol
# btrfs subvolume list /mnt-top
    ID 256 gen 9 top level 5 path @                # @ exists, gen 9 (created, ~empty)
# find /mnt-top -maxdepth 5 -name archwright
    /mnt-top/home/e2e/archwright                   # user home + staged files live in top-level
```

## Reproduction

1. Build: `go build -o archwright .`
2. Quick (no VM) — inspect the render:
   ```sh
   ./archwright install --only archinstall --yes --dry-run \
     --config test/e2e/vm/configs/btrfs-basic.yaml 2>&1 \
     | sed -n '/^{/,/^}/p' | python3 -m json.tool | less
   ```
   Confirm the root partition has `"mountpoint": "/"` **and** a `"btrfs"` entry with
   `"mountpoint": "/"`.
3. Full (real archinstall) — either:
   - `task vm-e2e -- btrfs-basic` and add a diagnostic, **or**
   - `sudo bash test/e2e/disks.sh --mode full --layout ...` against a btrfs config, then
     `btrfs subvolume get-default` / `btrfs subvolume list` the result.
   Observe the default subvolume is `ID 5` (top-level) and `@` is empty.

`test/e2e/vm/configs/btrfs-basic.yaml` currently uses a single `@` subvolume and the e2e
recipe mounts the bare partition precisely *because* of this bug (see
`test/e2e/vm/README.md` → "Finding: btrfs installs to the top-level subvolume"). A config
with a separate `@home` makes the breakage louder: the user home then lands in `@home`,
which the bare-partition mount doesn't expose.

## Expected behavior

The system should be installed **inside** the `@` subvolume and mounted with `subvol=@` at
`/` (the standard Arch/snapper layout), with `@home` at `/home`, etc. `btrfs subvolume
get-default` may remain `5`, but `/` must resolve to `@` (via fstab `subvol=@` and the
bootloader's `rootflags=subvol=@`), and the OS files must live in `@`, not the top-level.

## Actual behavior

The system is installed in the **top-level** subvolume (subvolid 5). `@` (and any `@home`,
`@log`) are created but empty and unused as mount roots.

## Likely root cause & fix direction

In archinstall's disk model, when a partition carries subvolumes that provide the
mountpoints, the **partition's own `mountpoint` should be `null`** — the subvolume entry
(`{"name": "@", "mountpoint": "/"}`) is what gets mounted at `/`. By emitting the root
partition with `mountpoint: "/"` *and* a subvolume mapping to `/`, archinstall mounts the
partition (top-level subvol) at `/` and the subvolume mapping is effectively ignored for the
root.

Investigate in `internal/archinstall/archinstall.go`:

- `btrfsBuilder.build()` → `singleDiskRoot(..., rootSpec{fsType:"btrfs", btrfs: subvols})`.
  `singleDiskRoot` sets the root partition `Mountpoint: &root` (`"/"`) unconditionally
  (around the `rootFs`/`Mountpoint: &root` assignment). For btrfs-with-subvolumes the
  partition `Mountpoint` should be `null` and the per-subvolume mountpoints should drive the
  mounts.
- Cross-check against archinstall 4.3 source for how a subvolumed btrfs partition is meant to
  be expressed (partition `mountpoint` null vs the `@`/`mountpoint:"/"` subvolume), and how it
  writes fstab + `rootflags=subvol=@` for the bootloader.
- This is a schema-shape change, so follow the CLAUDE.md two-commit rule (behavior-preserving
  refactor with goldens unchanged, then the shape change regenerating goldens) and the
  archinstall-drift gotcha (validate against a real archinstall run, not just the render).

## Validation after a fix

- `internal/archinstall` golden snapshots regenerate to show the btrfs root partition
  `mountpoint: null` with the `@`/`mountpoint:"/"` subvolume carrying the root.
- `task vm-e2e -- btrfs-basic` with a config that uses `@` + a separate `@home`, and a
  `root_mount` recipe of `mount -o subvol=@ /dev/vda2 /mnt` + `mount -o subvol=@home
  /dev/vda2 /mnt/home`, boots and passes Phase B (the staged binary is found under `@home`).
- On the booted system, `findmnt /` shows `subvol=/@` and `findmnt /home` shows `subvol=/@home`.
