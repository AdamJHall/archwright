# Feature / stage-coverage e2e descriptors — SEPARATE from the disk-layout matrix.
# Each runs on a minimal single-disk LVM layout (the layouts themselves are covered
# elsewhere) and exercises Phase A/B *stage features* instead, validated by
# lib/features.sh (validate_script) against the EXPECT_FEATURES token list.
#
# features-min bundles every feature that does NOT need a heavy desktop/flatpak/
# dotfiles/repo download, so the common case is one cheap VM run. Heavier features
# (flatpak runtimes, a KDE desktop, dotfiles, a custom repo) get their own
# descriptors so they can be run on demand — see the sibling matrix files.
_LVM_SINGLE_MOUNT = ["vgchange -ay vg0", "mount /dev/vg0/root /mnt"]

DESCRIPTORS = [
    {
        "name": "features-min",
        "config": "configs/features-min.yaml",
        "disks": ["12G"],
        "user": "e2e",
        "phase_b": True,
        "esp_part": "/dev/vda1",
        "root_mount": list(_LVM_SINGLE_MOUNT),
        "grub_serial": True,
        "validate_script": "lib/features.sh",
        "expect": {
            "FEATURES": "reflector kernel-zen plymouth hooks setup services",
            "HOSTNAME": "arch-e2e", "USER": "e2e",
        },
    },
]
