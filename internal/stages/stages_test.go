package stages

import (
	"strings"
	"testing"

	"github.com/AdamJHall/archwright/internal/config"
	"github.com/AdamJHall/archwright/internal/run"
	"gopkg.in/yaml.v3"
)

// A full, valid config exercising every stage's config-driven paths.
const testYAML = `
system:
  hostname: arch-box
  timezone: Europe/London
  locale: en_GB.UTF-8
  keymap: uk
user:
  name: adam
  shell: /usr/bin/zsh
  groups: [wheel, video]
disks:
  esp:
    device: /dev/nvme0n1
    size: 4GiB
  swap:
    size: 64GiB
  lvm:
    vg: vg0
    lv: root
    filesystem: xfs
    pvs: [/dev/nvme0n1p3, /dev/sda, /dev/sdb]
mirrors:
  reflector: true
  countries: [GB, DE]
  latest: 20
  sort: rate
  protocols: [https]
repos:
  - name: cachyos
    setup: |
      curl -fsSL https://mirror.cachyos.org/cachyos-repo.tar.xz | tar -xJ -C /tmp
      /tmp/cachyos-repo/cachyos-repo.sh --install
  - name: chaotic-aur
    key: 3056513887B78AEB
    keyserver: keyserver.ubuntu.com
    include: /etc/pacman.d/chaotic-mirrorlist
packages: [git, firefox]
kernel:
  packages: [linux-cachyos, linux-cachyos-headers]
  default: linux-cachyos
  replace_stock: true
flatpaks: [com.spotify.Client]
aur: [1password, 1password-cli]
plymouth:
  theme: bgrt
grub:
  cmdline_extra: "quiet splash"
  theme:
    source: vinceliuice
    name: tela
kde:
  look_and_feel: org.kde.breezedark.desktop
chezmoi:
  repo: https://github.com/AdamJHall/dotfiles
`

func testConfig(t *testing.T) *config.Config {
	t.Helper()
	var c config.Config
	if err := yaml.Unmarshal([]byte(testYAML), &c); err != nil {
		t.Fatalf("unmarshal test config: %v", err)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("test config should be valid: %v", err)
	}
	return &c
}

// planFor runs a single stage in dry-run and returns the recorded command plan.
func planFor(t *testing.T, phase Phase, name string) []string {
	t.Helper()
	ss := For(phase, name)
	if len(ss) != 1 {
		t.Fatalf("expected exactly one stage %q in phase %d, got %d", name, phase, len(ss))
	}
	r := &run.Runner{DryRun: true, Sudo: phase == Bootstrap}
	ctx := &Context{Cfg: testConfig(t), R: r, AssumeYes: true, ConfigPath: "/tmp/config.yaml"}
	if err := ss[0].Run(ctx); err != nil {
		t.Fatalf("stage %s returned error in dry-run: %v", name, err)
	}
	return r.Plan
}

func mustContain(t *testing.T, plan []string, subs ...string) {
	t.Helper()
	joined := strings.Join(plan, "\n")
	for _, s := range subs {
		if !strings.Contains(joined, s) {
			t.Errorf("plan missing %q.\nfull plan:\n%s", s, joined)
		}
	}
}

func TestRegistry(t *testing.T) {
	check := func(p Phase, want []string, orders []int) {
		ss := For(p, "")
		if len(ss) != len(want) {
			var got []string
			for _, s := range ss {
				got = append(got, s.Name())
			}
			t.Fatalf("phase %d: got stages %v, want %v", p, got, want)
		}
		for i, s := range ss {
			if s.Name() != want[i] {
				t.Errorf("phase %d position %d: got %q want %q", p, i, s.Name(), want[i])
			}
			if s.Order() != orders[i] {
				t.Errorf("stage %q: order %d want %d", s.Name(), s.Order(), orders[i])
			}
			if i > 0 && ss[i-1].Order() >= s.Order() {
				t.Errorf("stages not strictly ascending around %q", s.Name())
			}
		}
	}
	check(Install,
		[]string{"preflight", "archinstall"},
		[]int{0, 10})
	check(Bootstrap,
		[]string{"yay", "packages", "flatpak", "aur", "plymouth", "grub-theme", "kde", "chezmoi"},
		[]int{10, 20, 30, 40, 50, 60, 70, 80})
}

func TestPlan_Archinstall(t *testing.T) {
	plan := planFor(t, Install, "archinstall")
	mustContain(t, plan,
		// reflector picks mirrors before pacstrap
		"reflector --country GB,DE --latest 20 --sort rate --protocol https --save /etc/pacman.d/mirrorlist",
		// delegates the install to archinstall with our generated files
		"archinstall --config "+aiConfigPath+" --creds "+aiCredsPath+" --silent",
		// remounts target + ESP for the chroot post-install work
		"mount /dev/vg0/root /mnt",
		"mount /dev/nvme0n1p1 /mnt/boot",
		// repos configured in the chroot (persist into the target)
		"arch-chroot /mnt pacman-key --recv-keys 3056513887B78AEB --keyserver keyserver.ubuntu.com",
		"arch-chroot /mnt pacman-key --lsign-key 3056513887B78AEB",
		"cachyos-repo.sh --install",
		"arch-chroot /mnt pacman -Sy",
		// custom kernel installed, stock removed, default pinned, GRUB regenerated
		"arch-chroot /mnt pacman -S --needed --noconfirm linux-cachyos linux-cachyos-headers",
		"arch-chroot /mnt pacman -Rns --noconfirm linux",
		`GRUB_TOP_LEVEL="/boot/vmlinuz-linux-cachyos"`,
		"arch-chroot /mnt grub-mkconfig -o /boot/grub/grub.cfg",
		// stages the binary + config into the new user's home for Phase B
		"/mnt/home/adam/archwright",
		"arch-chroot /mnt chown -R adam:adam /home/adam",
	)
}

func TestWipedDevices(t *testing.T) {
	got := wipedDevices(testConfig(t))
	want := []string{"/dev/nvme0n1", "/dev/sda", "/dev/sdb"} // disk1 + whole-disk PVs
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("wipedDevices = %v, want %v", got, want)
	}
}

func TestPlan_Packages(t *testing.T) {
	mustContain(t, planFor(t, Bootstrap, "packages"),
		"pacman -S --needed --noconfirm git firefox",
	)
}

func TestPlan_AUR(t *testing.T) {
	mustContain(t, planFor(t, Bootstrap, "aur"),
		"yay -S --needed --noconfirm 1password 1password-cli",
	)
}

func TestPlan_Flatpak(t *testing.T) {
	mustContain(t, planFor(t, Bootstrap, "flatpak"),
		"flatpak remote-add --if-not-exists flathub",
		"flatpak install -y --noninteractive flathub com.spotify.Client",
	)
}

func TestPlan_Plymouth(t *testing.T) {
	mustContain(t, planFor(t, Bootstrap, "plymouth"),
		"plymouth-set-default-theme -R bgrt",
		"grub-mkconfig -o /boot/grub/grub.cfg",
	)
}

func TestPlan_GrubTheme(t *testing.T) {
	mustContain(t, planFor(t, Bootstrap, "grub-theme"),
		"git clone --depth 1 https://github.com/vinceliuice/grub2-themes",
		"install.sh -t tela",
	)
}

func TestPlan_Chezmoi(t *testing.T) {
	// init or apply depending on host state; both mention the repo or 'chezmoi'.
	mustContain(t, planFor(t, Bootstrap, "chezmoi"), "chezmoi")
}

// kde and yay are host-state dependent (need plasma tools / yay absence); they
// must at least run without error in dry-run.
func TestPlan_NoErrorStages(t *testing.T) {
	for _, name := range []string{"kde", "yay"} {
		_ = planFor(t, Bootstrap, name)
	}
}
