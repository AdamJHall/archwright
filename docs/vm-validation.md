# VM validation of the rendered archinstall config

This was the last open item before trusting archwright on real hardware. The automated harness
below (`test/e2e/vm/`) now closes it: every reverse-engineered shape has been driven through a
real archinstall 4.3 + boot + bootstrap run. Most are confirmed working; three surfaced real
bugs (now tracked in [`docs/bugs/`](bugs/)). See the results table below.

archinstall's config JSON is **not a stable API** — its schema changes between releases. We
render against the pinned `Version` in `internal/archinstall/archinstall.go` (currently
`4.3`), and the JSON shape (LVM, swap, encryption, bootloader, PV `obj_id` wiring, creds
keys) was **reverse-engineered from archinstall source**. The render tests
(`go test ./internal/archinstall/`, the golden snapshots, and the `e2e` / `e2e-disks`
workflows) prove our output is *stable and self-consistent*, and `e2e-disks` confirms a real
archinstall *parses* it against loopback devices — but none of that proves a real archinstall
*does the right thing with it end-to-end on a booted system*.

That last mile needs a QEMU run that boots a real systemd live ISO, feeds it the rendered
config, and verifies the machine partitions, installs, and **boots and runs Phase B
end-to-end**.

## Automated harness — `test/e2e/vm/`

`test/e2e/vm/e2e.py` (run it with `task vm-e2e -- <name>`, or `task vm-e2e` for the whole
matrix; `task vm-e2e-list` lists them) does this **fully unattended**: it boots the ISO
headless on a serial console, runs `archwright install --yes`, injects a harness-only
Phase-B autorun (a serial autologin + bootstrap+validate trigger that lives **only** in the
test scaffold, never in a real config), reboots from disk, runs `archwright bootstrap`, and
asserts the installed system with a parametrized `lib/validate.sh`. See
`test/e2e/vm/README.md` for the descriptor contract and how to add coverage. The matrix
(`test/e2e/vm/matrix/*.py` + `configs/*.yaml`) covers lvm (single / multi / multi-volume),
btrfs (+ compress / snapper), plain (every swap type), systemd-boot, and the LUKS layouts
(which validate the on-disk encryption on the ISO, since encrypted Phase B staging is not
yet implemented).

Status (full matrix run, archinstall 4.3): **14 descriptors green**, **3 distinct real bugs
found**. Green end-to-end (install → reboot → bootstrap → validate): all lvm single-LV layouts,
all plain layouts × every swap type, btrfs (basic + snapper), the feature/stage-coverage runs,
and both encryption layouts (validated on the ISO — see the encryption note below). The bugs
are in `docs/bugs/` and the results table maps each to its shape.

Two things the harness does **not** prove, by design:
- **Graphical desktop rendering.** It validates boot → multi-user → `bootstrap` → assertions,
  not that a KDE session visually renders (the trimmed configs mostly use
  `desktop.environment: none`; `features-desktop` only checks the plasma tooling installed +
  the stage ran). Use `task vm-disk` to watch a real desktop come up by hand.
- **A full encrypted boot.** archwright's Phase B staging is skipped for encrypted installs
  (the LUKS remount isn't implemented), so the encryption descriptors assert the on-disk LUKS
  shape on the live ISO (container present + passphrase unlocks) rather than booting the
  encrypted system and running `bootstrap`.

The older `test/vm.sh` (`task vm` / `vm-fresh` / `vm-disk`) remains for **interactive**
poking at a VM by hand (including the desktop-render check above).

> Use `-cpu host` for local VM runs — otherwise the CachyOS repo setup skips and
> `linux-cachyos` fails with "target not found". (The e2e matrix configs use the stock
> `linux` kernel and no CachyOS repo, so they are unaffected; this matters for configs that
> add `linux-cachyos`.)

## Results — reverse-engineered shapes vs a real archinstall 4.3 run

Each shape was reverse-engineered; the harness has now exercised them all.

| Area | Shape | Result |
|------|-------|--------|
| Bootloader | `bootloader_config: {bootloader, uki, removable}` field names/casing | ✅ confirmed — grub (`grub.cfg`, boots) and systemd-boot both install + boot |
| Swap | `partition` (`fs_type: linux-swap`, flag `swap`), zram, swapfile | ✅ confirmed — swapfile (lvm/plain), zram (btrfs, plain-zram), partition (plain-swappart) all active post-boot |
| Encryption | nested `disk_config.disk_encryption` (`encryption_type` + `partitions`); `encryption_password` casing | ✅ confirmed — `enc-lvm` (lvm_on_luks) + `enc-luks-plain` (luks): LUKS container present and the passphrase unlocks (`luksOpen --test-passphrase`). `lvm_on_luks` >2-PV limit not separately exercised; full encrypted Phase B still unimplemented in archwright |
| Snapper | timer-unit + `set-config` key names | ✅ confirmed — `btrfs-snapper` installs snapper + green |
| Btrfs | subvolume JSON `{name, mountpoint}` | ❌ **bug** — shape parses, but archinstall installs to the **top-level** subvolume; the configured `@` is created but unused as root → [`btrfs-subvolume-not-used-as-root.md`](bugs/btrfs-subvolume-not-used-as-root.md) |
| LVM | multi-volume "rest of VG" sizing (fixed root + remainder `/home`) | ❌ **bug** — the PV partition renders with an empty `fs_type`; archinstall aborts Phase A before sizing is reached → [`lvm-multivolume-pv-fstype-empty.md`](bugs/lvm-multivolume-pv-fstype-empty.md) |
| systemd-boot | loader-entry default + `bootctl update` cmdline-refresh path | ⚠️ install + boot **work**; the Phase-B `bootctl update` refresh path **fails** (nonzero when already current), reached via the always-on plymouth stage → [`plymouth-bootctl-update-fails-systemd-boot.md`](bugs/plymouth-bootctl-update-fails-systemd-boot.md) |

(A fourth bug unrelated to a disk shape — the flatpak stage hangs on a polkit prompt — is in
[`flatpak-system-remote-add-polkit-hang.md`](bugs/flatpak-system-remote-add-polkit-hang.md).)

## After an archinstall version bump

Diff the upstream schema and update `internal/archinstall/` **and** the `Version` constant
together, then re-run the VM validation above. Preflight only *warns* on a version mismatch;
it does not block.
