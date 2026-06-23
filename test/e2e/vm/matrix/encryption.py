# LUKS encryption e2e descriptors. These are reverse-engineered + VM-validation-
# pending (see CLAUDE.md archinstall-drift rule): archwright's encrypted-install
# postInstall deliberately skips Phase B staging until a LUKS remount is
# implemented, so these descriptors set phase_b False and instead assert the
# on-disk LUKS layout on the live ISO via `iso_validate` (each command must exit
# 0; the LUKS passphrase is the --yes throwaway password "installme"). For both
# layouts the encrypted partition is vda2 (the PV partition for lvm_on_luks, the
# single root partition for luks).
DESCRIPTORS = [
    {
        "name": "enc-lvm",
        "config": "configs/enc-lvm.yaml",
        "disks": ["12G"],
        "user": "e2e",
        "phase_b": False,
        "esp_part": "/dev/vda1",
        # vda2 must be a LUKS container, and the throwaway install passphrase must
        # unlock a keyslot. `--test-passphrase` reads stdin as a PASSPHRASE (no
        # trailing `-`, which would mean a keyfile) and creates no mapping, so it
        # is immune to the install leaving the VG/dm-crypt mapped.
        "iso_validate": [
            "cryptsetup isLuks /dev/vda2",
            "echo -n installme | cryptsetup luksOpen --test-passphrase /dev/vda2",
        ],
        "expect": {"LAYOUT": "lvm", "ENCRYPTION": "1"},
    },
    {
        "name": "enc-luks-plain",
        "config": "configs/enc-luks-plain.yaml",
        "disks": ["12G"],
        "user": "e2e",
        "phase_b": False,
        "esp_part": "/dev/vda1",
        "iso_validate": [
            "cryptsetup isLuks /dev/vda2",
            "echo -n installme | cryptsetup luksOpen --test-passphrase /dev/vda2",
        ],
        "expect": {"LAYOUT": "plain", "ENCRYPTION": "1"},
    },
]
