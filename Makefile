# Convenience targets. The Go workflow stays `go build/test/vet`; these wrap the
# Tier 2 loopback integration harness, which needs root (losetup/lvm/archinstall).

.PHONY: build test vet e2e-disks-light e2e-disks-full vm vm-fresh vm-disk

build:
	go build -o archwright .

test:
	go test ./...

vet:
	go vet ./...

# Light: validate archwright's rendered archinstall JSON against a real
# archinstall (--dry-run) on loop devices. No install, no network.
#   make e2e-disks-light                       # default multi-disk + xfs
#   make e2e-disks-light LAYOUT=single-disk-lvm FS=ext4
LAYOUT ?= multi-disk-lvm
FS     ?= xfs
e2e-disks-light:
	sudo bash test/e2e/disks.sh --mode light --layout $(LAYOUT) --fs $(FS)

# Full: real partition/format/pacstrap onto loop devices, then assert layout.
# Slow; needs network + disk space.
e2e-disks-full:
	sudo bash test/e2e/disks.sh --mode full --layout $(LAYOUT) --fs $(FS) \
		--disk1-size 12G --extra-size 6G

# Interactive QEMU smoke test of the full flow. Builds + boots a UEFI VM with
# the repo shared in over 9p (binary + config show up inside the live ISO).
#   make vm          # boot the Arch live ISO (run Phase A)
#   make vm-fresh    # same, but wipe the virtual disks first
#   make vm-disk     # boot the installed system off disk 1
vm:
	bash test/vm.sh iso
vm-fresh:
	bash test/vm.sh iso --fresh
vm-disk:
	bash test/vm.sh disk
