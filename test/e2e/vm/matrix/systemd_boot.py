# systemd-boot bootloader e2e descriptors. See e2e.py's load_matrix() for the schema.
# These exist to VM-validate the reverse-engineered systemd-boot archinstall path
# (install + boot end-to-end) across the lvm and plain layouts. grub_serial is False:
# there is no GRUB to edit with systemd-boot; the serial-getty autologin still fires.
DESCRIPTORS = [
    {
        "name": "sdboot-lvm",
        "config": "configs/sdboot-lvm.yaml",
        "disks": ["12G"],
        "user": "e2e",
        "phase_b": True,
        "esp_part": "/dev/vda1",
        "root_mount": [
            "vgchange -ay vg0",
            "mount /dev/vg0/root /mnt",
        ],
        "grub_serial": False,
        "expect": {
            "LAYOUT": "lvm", "ROOT_FS": "ext4", "SWAP": "swapfile",
            "BOOTLOADER": "systemd-boot", "HOSTNAME": "arch-e2e", "USER": "e2e",
            "VG": "vg0", "LV": "root", "PV_COUNT": "1",
            "PACKAGES": "tree jq", "AUR_HELPER": "yay", "ENCRYPTION": "0",
        },
    },
    {
        "name": "sdboot-plain",
        "config": "configs/sdboot-plain.yaml",
        "disks": ["12G"],
        "user": "e2e",
        "phase_b": True,
        "esp_part": "/dev/vda1",
        "root_mount": [
            "mount /dev/vda2 /mnt",
        ],
        "grub_serial": False,
        "expect": {
            "LAYOUT": "plain", "ROOT_FS": "ext4", "SWAP": "swapfile",
            "BOOTLOADER": "systemd-boot", "HOSTNAME": "arch-e2e", "USER": "e2e",
            "PACKAGES": "tree jq", "AUR_HELPER": "yay", "ENCRYPTION": "0",
        },
    },
]
