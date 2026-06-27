package stages

import "testing"

// These cover the Phase-A postInstall additions:
//   - user.shell is applied to the installed user via chsh (it was previously
//     defined-but-unused, so installs defaulted to /bin/bash),
//   - pacman.multilib uncomments the [multilib] repo and syncs the new db so
//     32-bit packages (steam) resolve in Phase B.
// Self-contained YAML keeps them off the shared fixtures.

const shellMultilibYAML = `
system: {hostname: arch-sm, timezone: Europe/London, locale: en_GB.UTF-8, keymap: uk}
user: {name: adam, shell: /usr/bin/zsh}
pacstrap: [base-devel, git, zsh, sudo, networkmanager, efibootmgr, intel-ucode]
kernel: {base: [linux]}
pacman: {multilib: true}
disks:
  layout: plain
  esp: {device: /dev/nvme0n1, size: 1GiB}
  swap: {type: none}
  plain: {device: /dev/nvme0n1, filesystem: ext4}
`

func TestPostInstall_AppliesUserShellAndMultilib(t *testing.T) {
	plan := runArchinstall(t, shellMultilibYAML)
	mustContain(t, plan,
		"arch-chroot /mnt chsh -s /usr/bin/zsh adam",
		`sed -i '/^#\[multilib\]/,/^#Include/ s/^#//' /etc/pacman.conf`,
		"arch-chroot /mnt pacman -Sy",
	)
}

const noShellNoMultilibYAML = `
system: {hostname: arch-ns, timezone: Europe/London, locale: en_GB.UTF-8, keymap: uk}
user: {name: adam}
pacstrap: [base-devel, git, zsh, sudo, networkmanager, efibootmgr, intel-ucode]
kernel: {base: [linux]}
disks:
  layout: plain
  esp: {device: /dev/nvme0n1, size: 1GiB}
  swap: {type: none}
  plain: {device: /dev/nvme0n1, filesystem: ext4}
`

// Unset shell/multilib preserve today's behavior: no chsh, no pacman.conf edit.
func TestPostInstall_UnsetShellAndMultilibAreNoOps(t *testing.T) {
	plan := runArchinstall(t, noShellNoMultilibYAML)
	mustNotContain(t, plan,
		"chsh",
		"multilib",
	)
}
