# VM validation of the rendered archinstall config — outstanding work

This is the one open item before trusting archwright on real hardware.

archinstall's config JSON is **not a stable API** — its schema changes between releases. We
render against the pinned `Version` in `internal/archinstall/archinstall.go` (currently
`4.3`), and the JSON shape (LVM, swap, encryption, bootloader, PV `obj_id` wiring, creds
keys) was **reverse-engineered from archinstall source**. The render tests
(`go test ./internal/archinstall/`, the golden snapshots, and the `e2e` / `e2e-disks`
workflows) prove our output is *stable and self-consistent*, and `e2e-disks` confirms a real
archinstall *parses* it against loopback devices — but none of that proves a real archinstall
*does the right thing with it end-to-end on a booted system*.

That last mile needs a QEMU run that boots a real systemd live ISO, feeds it the rendered
config, and verifies the machine partitions, installs, and **boots to a desktop**. The
harness exists (`test/vm.sh`, `task vm` / `task vm-fresh` / `task vm-disk`); what remains is
to actually run each layout/feature through it and confirm the reverse-engineered shapes
below.

> Use `-cpu host` for local VM runs — otherwise the CachyOS repo setup skips and
> `linux-cachyos` fails with "target not found".

## Shapes to confirm against a real archinstall 4.3 run

Each was reverse-engineered and is unproven on hardware. Validate, then delete its row here.

| Area | Shape to confirm |
|------|------------------|
| Bootloader | `bootloader_config: {bootloader, uki, removable}` field names/casing |
| Btrfs | subvolume JSON `{name, mountpoint}` — whether archinstall wants extra keys (per-subvol compression, `nodatacow`); `disk_config.btrfs_options` is intentionally not emitted |
| Swap | `partition` shape (`fs_type: linux-swap`, flag `swap`); zram and swapfile paths |
| Encryption | nested `disk_config.disk_encryption` (`encryption_type` + `partitions`) obj_id wiring; `encryption_password` casing; the `lvm_on_luks` >2-partition limit |
| systemd-boot | loader-entry default + the `bootctl update` cmdline-refresh path |
| LVM | multi-volume "rest of VG" sizing (fixed root + remainder-taking `/home`) |
| Snapper | timer-unit + `set-config` key names (`snapper-timeline.timer`, `snapper-cleanup.timer`, `TIMELINE_LIMIT_*`) |

## After an archinstall version bump

Diff the upstream schema and update `internal/archinstall/` **and** the `Version` constant
together, then re-run the VM validation above. Preflight only *warns* on a version mismatch;
it does not block.
