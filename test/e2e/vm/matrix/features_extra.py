# Heavier feature / stage-coverage e2e descriptors — SEPARATE from both the disk
# layout matrix (matrix/lvm.py et al.) and the cheap feature bundle (matrix/features.py).
#
# features-min bundles every feature that needs no heavy download. These four pull
# real artifacts (a flatpak runtime, a dotfiles repo, a custom repo key, a Plasma
# desktop), so they get their own on-demand descriptors. All share the same minimal
# single-disk LVM layout as configs/features-min.yaml; each exercises one Phase A/B
# stage feature, validated by lib/features.sh against its EXPECT_FEATURES token.
_LVM_SINGLE_MOUNT = ["vgchange -ay vg0", "mount /dev/vg0/root /mnt"]

DESCRIPTORS = [
    {
        "name": "features-flatpak",
        "config": "configs/features-flatpak.yaml",
        "disks": ["12G"],
        "user": "e2e",
        "phase_b": True,
        "esp_part": "/dev/vda1",
        "root_mount": list(_LVM_SINGLE_MOUNT),
        "grub_serial": True,
        "validate_script": "lib/features.sh",
        "expect": {
            "FEATURES": "flatpak",
            "FLATPAK_APP": "com.github.tchx84.Flatseal",
            "HOSTNAME": "arch-e2e", "USER": "e2e",
        },
    },
    {
        "name": "features-dotfiles",
        "config": "configs/features-dotfiles.yaml",
        "disks": ["12G"],
        "user": "e2e",
        "phase_b": True,
        "esp_part": "/dev/vda1",
        "root_mount": list(_LVM_SINGLE_MOUNT),
        "grub_serial": True,
        "validate_script": "lib/features.sh",
        # Inject a tiny local chezmoi source repo here so the config's
        # file:///home/e2e/dots resolves offline + deterministically.
        "inject_repo": "/home/e2e/dots",
        "expect": {
            "FEATURES": "dotfiles",
            "HOSTNAME": "arch-e2e", "USER": "e2e",
        },
    },
    {
        "name": "features-repos",
        "config": "configs/features-repos.yaml",
        "disks": ["12G"],
        "user": "e2e",
        "phase_b": True,
        "esp_part": "/dev/vda1",
        "root_mount": list(_LVM_SINGLE_MOUNT),
        "grub_serial": True,
        "validate_script": "lib/features.sh",
        "expect": {
            "FEATURES": "repos",
            "REPO_KEY": "3056513887B78AEB",
            "HOSTNAME": "arch-e2e", "USER": "e2e",
        },
    },
    {
        # Heaviest descriptor: pulls a Plasma desktop (plasma-desktop ->
        # plasma-workspace) just to get the plasma-apply-* helpers. The KDE stage
        # runs those helpers, which normally expect a running Plasma session; run
        # headless they may behave differently and the stage only warns on failure,
        # so this is best-effort — features.sh asserts the color scheme landed in
        # ~/.config/kdeglobals when it does.
        "name": "features-desktop",
        "config": "configs/features-desktop.yaml",
        "disks": ["12G"],
        "user": "e2e",
        "phase_b": True,
        "esp_part": "/dev/vda1",
        "root_mount": list(_LVM_SINGLE_MOUNT),
        "grub_serial": True,
        "validate_script": "lib/features.sh",
        "expect": {
            "FEATURES": "desktop",
            "KDE_COLORSCHEME": "BreezeDark",
            "HOSTNAME": "arch-e2e", "USER": "e2e",
        },
    },
]
