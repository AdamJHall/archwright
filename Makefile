# Convenience targets. The Go workflow stays `go build/test/vet`; these wrap the
# Tier 2 loopback integration harness, which needs root (losetup/lvm/archinstall).

.PHONY: build test vet e2e-disks-light e2e-disks-full

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
