# Bug: multi-volume LVM renders the PV partition with an empty `fs_type`; archinstall aborts

**Status:** open — found by the automated VM e2e harness (`test/e2e/vm/`, descriptor `lvm-volumes`), 2026-06-23
**Area:** `internal/archinstall/archinstall.go` — `lvmBuilder.build()` (the PV partition `fs_type`)
**Severity:** high — the **multi-volume LVM layout does not install at all**; Phase A archinstall
crashes before partitioning completes.

## Summary

For the `lvm` layout in **multi-volume mode** (`disks.lvm.volumes:` set instead of
`lv`+`filesystem`), archwright renders the LVM **PV partition** with `fs_type: ""` (empty
string). A real archinstall 4.3 run rejects that with
`ValueError: File system type is not set` while creating partitions, so the install aborts.

Single-LV mode (`disks.lvm.lv` + `disks.lvm.filesystem`) works because the PV partition's
`fs_type` is set to the LV filesystem (e.g. `xfs`).

## Root cause

`internal/archinstall/archinstall.go`, in `lvmBuilder.build()`:

```go
// A PV partition carries the LV filesystem as its fs_type purely so parted can
// create it (archinstall 4.x requires a non-null fs_type per partition); the
// filesystem is never written, the partition is pvcreated.
pvFs := b.lvm.Filesystem          // <-- empty in multi-volume mode
...
disk1PVPart := Partition{..., FsType: &pvFs, ...}
```

`b.lvm.Filesystem` is **only set in single-LV mode**. In multi-volume mode the schema
*requires it to be empty* (`config.go` `lvmVolumeErrors`: "set either lv+filesystem OR
volumes, not both"), and each volume carries its own `filesystem`. So `pvFs == ""`, and every
PV partition (disk-1 PV and any whole-disk PVs, which reuse the same `pvFs`) is emitted with
an empty `fs_type`.

## Evidence

### Rendered partitions (`--dry-run`)

`./archwright install --only archinstall --yes --dry-run --config <cfg>`:

| config (mode)                   | PV partition `fs_type` |
|---------------------------------|------------------------|
| `lvm-single` (single-LV, xfs)   | `"xfs"`  → installs OK |
| `lvm-volumes` (multi-volume)    | `""`     → **aborts**  |

The volumes themselves are fine (`root`→xfs, `home`→ext4); only the PV partition is wrong.

### archinstall traceback (from the VM run)

```
Creating partitions: /dev/vda
  File ".../archinstall/lib/disk/device_handler.py", line 373, in _setup_partition
    fs_value = part_mod.safe_fs_type.parted_value
  File ".../archinstall/lib/models/device.py", line 897, in safe_fs_type
    raise ValueError('File system type is not set')
ValueError: File system type is not set
```

## Reproduction

1. `go build -o archwright .`
2. Render-only: `./archwright install --only archinstall --yes --dry-run --config
   test/e2e/vm/configs/lvm-volumes.yaml 2>&1 | sed -n '/^{/,/^}/p' | python3 -m json.tool`
   → the second partition on `/dev/vda` has `"fs_type": ""`.
3. Full: `task vm-e2e -- lvm-volumes` (or `test/e2e/disks.sh` with a multi-volume config)
   → archinstall aborts with the traceback above.

## Expected behavior

Multi-volume LVM installs successfully, with `root`/`home`/… LVs formatted per their
configured filesystems.

## Actual behavior

archinstall aborts in Phase A with `ValueError: File system type is not set`; nothing is
installed.

## Fix direction

Give the PV partition a valid non-empty `fs_type` even in multi-volume mode. The comment
already notes the value is cosmetic ("the filesystem is never written, the partition is
pvcreated"), so any valid fs works. Options:

- Fall back to a volume's filesystem when the top-level one is empty, e.g.
  ```go
  pvFs := b.lvm.Filesystem
  if pvFs == "" && len(b.lvm.Volumes) > 0 {
      pvFs = b.lvm.Volumes[0].Filesystem
  }
  ```
- Or use a fixed placeholder (e.g. `"ext4"`) for PV partitions regardless of mode (and
  consider doing the same in single-LV mode, since `xfs` on a PV partition is equally
  cosmetic).

Add a render golden + a `config_test.go`/`golden_test.go` case for the multi-volume layout so
this is covered, and follow the CLAUDE.md archinstall-drift rule (validate against a real
archinstall run — `task vm-e2e -- lvm-volumes` should reach Phase B and pass).

## Note for the e2e descriptor

Once fixed, `lvm-volumes` Phase B will need its `root_mount` to also mount the `home` LV
(the user home lives on a separate LV, so the staged binary/config under `/home/<user>` are
only reachable after `mount /dev/<vg>/home /mnt/home`) — the same separate-`/home` mount
concern noted for the btrfs `@home` case. Update `test/e2e/vm/matrix/lvm_variants.py`
accordingly when validating the fix.
