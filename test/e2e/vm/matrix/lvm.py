# LVM-layout e2e descriptors. See e2e.py's load_matrix() for the schema.
DESCRIPTORS = [
    {
        "name": "lvm-multi",
        "config": "configs/lvm-multi.yaml",
        "disks": ["12G", "4G", "4G"],
        "user": "e2e",
        "phase_b": True,
        "esp_part": "/dev/vda1",
        "root_mount": [
            "vgchange -ay vg0",
            "mount /dev/vg0/root /mnt",
        ],
        "grub_serial": True,
        "expect": {
            "LAYOUT": "lvm", "ROOT_FS": "ext4", "SWAP": "swapfile",
            "BOOTLOADER": "grub", "HOSTNAME": "arch-e2e", "USER": "e2e",
            "VG": "vg0", "LV": "root", "PV_COUNT": "3",
            "PACKAGES": "tree jq", "AUR_HELPER": "yay", "ENCRYPTION": "0",
        },
    },
]
