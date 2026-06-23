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
        # The system is installed inside the @ subvolume (archwright renders the
        # btrfs root partition with a null mountpoint so @ drives /), so the
        # scaffold injection must mount @, matching archwright's own postInstall.
        # Mounting the bare partition would expose only the empty top-level subvol.
        "root_mount": [
            "mount -o subvol=@ /dev/vda2 /mnt",
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
        # System installed inside @ (see btrfs-basic); mount @ for scaffold inject.
        "root_mount": [
            "mount -o subvol=@ /dev/vda2 /mnt",
        ],
        "grub_serial": True,
        "expect": {
            "LAYOUT": "btrfs", "ROOT_FS": "btrfs", "SWAP": "zram",
            "BOOTLOADER": "grub", "HOSTNAME": "arch-e2e", "USER": "e2e",
            "PACKAGES": "tree jq snapper", "AUR_HELPER": "yay", "ENCRYPTION": "0",
        },
    },
]
