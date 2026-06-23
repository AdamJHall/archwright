# btrfs-layout e2e descriptors. See e2e.py's load_matrix() for the schema.
# Single virtio disk (vda): ESP p1 + btrfs root p2 carrying subvolumes; the root
# is mounted via its `@` subvolume. swap is zram (no on-disk swap for btrfs).
DESCRIPTORS = [
    {
        "name": "btrfs-basic",
        "config": "configs/btrfs-basic.yaml",
        "disks": ["12G"],
        "user": "e2e",
        "phase_b": True,
        "esp_part": "/dev/vda1",
        # archinstall installs the system into the btrfs TOP-LEVEL subvolume (the
        # default, subvolid 5) — the configured @ is created but not used as root —
        # so we mount the bare partition (its default subvol), matching archwright's
        # own postInstall/rootDevice. (That the named subvolumes aren't the root
        # mount is a separate btrfs finding noted in README.md.)
        "root_mount": [
            "mount /dev/vda2 /mnt",
        ],
        "grub_serial": True,
        "expect": {
            "LAYOUT": "btrfs", "ROOT_FS": "btrfs", "SWAP": "zram",
            "BOOTLOADER": "grub", "HOSTNAME": "arch-e2e", "USER": "e2e",
            "PACKAGES": "tree jq", "AUR_HELPER": "yay", "ENCRYPTION": "0",
        },
    },
    {
        "name": "btrfs-snapper",
        "config": "configs/btrfs-snapper.yaml",
        "disks": ["12G"],
        "user": "e2e",
        "phase_b": True,
        "esp_part": "/dev/vda1",
        "root_mount": [
            "mount /dev/vda2 /mnt",
        ],
        "grub_serial": True,
        "expect": {
            "LAYOUT": "btrfs", "ROOT_FS": "btrfs", "SWAP": "zram",
            "BOOTLOADER": "grub", "HOSTNAME": "arch-e2e", "USER": "e2e",
            "PACKAGES": "tree jq snapper", "AUR_HELPER": "yay", "ENCRYPTION": "0",
        },
    },
]
