# Plain-layout e2e descriptors. See e2e.py's load_matrix() for the schema.
# Exercises every swap type on a single-disk ESP + plain root. With a swap
# partition the root lands on vda3 (swap is p2); otherwise root is vda2.
DESCRIPTORS = [
    {
        "name": "plain-ext4",
        "config": "configs/plain-ext4.yaml",
        "disks": ["12G"],
        "user": "e2e",
        "phase_b": True,
        "esp_part": "/dev/vda1",
        "root_mount": [
            "mount /dev/vda2 /mnt",
        ],
        "grub_serial": True,
        "expect": {
            "LAYOUT": "plain", "ROOT_FS": "ext4", "SWAP": "swapfile",
            "BOOTLOADER": "grub", "HOSTNAME": "arch-e2e", "USER": "e2e",
            "PACKAGES": "tree jq", "AUR_HELPER": "yay", "ENCRYPTION": "0",
        },
    },
    {
        "name": "plain-xfs",
        "config": "configs/plain-xfs.yaml",
        "disks": ["12G"],
        "user": "e2e",
        "phase_b": True,
        "esp_part": "/dev/vda1",
        "root_mount": [
            "mount /dev/vda2 /mnt",
        ],
        "grub_serial": True,
        "expect": {
            "LAYOUT": "plain", "ROOT_FS": "xfs", "SWAP": "none",
            "BOOTLOADER": "grub", "HOSTNAME": "arch-e2e", "USER": "e2e",
            "PACKAGES": "tree jq", "AUR_HELPER": "yay", "ENCRYPTION": "0",
        },
    },
    {
        "name": "plain-zram",
        "config": "configs/plain-zram.yaml",
        "disks": ["12G"],
        "user": "e2e",
        "phase_b": True,
        "esp_part": "/dev/vda1",
        "root_mount": [
            "mount /dev/vda2 /mnt",
        ],
        "grub_serial": True,
        "expect": {
            "LAYOUT": "plain", "ROOT_FS": "ext4", "SWAP": "zram",
            "BOOTLOADER": "grub", "HOSTNAME": "arch-e2e", "USER": "e2e",
            "PACKAGES": "tree jq", "AUR_HELPER": "yay", "ENCRYPTION": "0",
        },
    },
    {
        "name": "plain-swappart",
        "config": "configs/plain-swappart.yaml",
        "disks": ["12G"],
        "user": "e2e",
        "phase_b": True,
        "esp_part": "/dev/vda1",
        "root_mount": [
            "mount /dev/vda3 /mnt",
        ],
        "grub_serial": True,
        "expect": {
            "LAYOUT": "plain", "ROOT_FS": "ext4", "SWAP": "partition",
            "BOOTLOADER": "grub", "HOSTNAME": "arch-e2e", "USER": "e2e",
            "PACKAGES": "tree jq", "AUR_HELPER": "yay", "ENCRYPTION": "0",
        },
    },
]
