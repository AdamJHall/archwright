# LVM-layout variant e2e descriptors: single-disk single-LV (xfs root) and
# single-disk multi-volume (fixed xfs root + rest-of-VG ext4 /home).
# See e2e.py's load_matrix() for the schema.
DESCRIPTORS = [
    {
        "name": "lvm-single",
        "config": "configs/lvm-single.yaml",
        "disks": ["12G"],
        "user": "e2e",
        "phase_b": True,
        "esp_part": "/dev/vda1",
        "root_mount": [
            "vgchange -ay vg0",
            "mount /dev/vg0/root /mnt",
        ],
        "grub_serial": True,
        "expect": {
            "LAYOUT": "lvm", "ROOT_FS": "xfs", "SWAP": "swapfile",
            "BOOTLOADER": "grub", "HOSTNAME": "arch-e2e", "USER": "e2e",
            "VG": "vg0", "LV": "root", "PV_COUNT": "1",
            "PACKAGES": "tree jq", "AUR_HELPER": "yay", "ENCRYPTION": "0",
        },
    },
    {
        "name": "lvm-volumes",
        "config": "configs/lvm-volumes.yaml",
        "disks": ["12G"],
        "user": "e2e",
        "phase_b": True,
        "esp_part": "/dev/vda1",
        "root_mount": [
            "vgchange -ay vg0",
            "mount /dev/vg0/root /mnt",
        ],
        "grub_serial": True,
        "expect": {
            "LAYOUT": "lvm", "ROOT_FS": "xfs", "SWAP": "swapfile",
            "BOOTLOADER": "grub", "HOSTNAME": "arch-e2e", "USER": "e2e",
            "VG": "vg0", "LV": "root", "PV_COUNT": "1",
            "PACKAGES": "tree jq", "AUR_HELPER": "yay", "ENCRYPTION": "0",
        },
    },
]
