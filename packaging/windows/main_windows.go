package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

type configuration struct {
	Database struct {
		Host     string `json:"host"`
		Port     int    `json:"port"`
		User     string `json:"user"`
		Password string `json:"password"`
		DBName   string `json:"dbname"`
		SSLMode  string `json:"sslmode"`
	} `json:"database"`
	SkillDir string `json:"skill_dir"`
}

func main() {
	addr := flag.String("addr", "127.0.0.1:8787", "HTTP listen address")
	proxy := flag.String("proxy", "127.0.0.1:8788", "Traffic recording proxy address")
	noBrowser := flag.Bool("no-browser", false, "Do not open the browser")
	flag.Parse()
	if err := run(*addr, *proxy, *noBrowser); err != nil {
		fmt.Fprintln(os.Stderr, "[ARTEX]", err)
		os.Exit(1)
	}
}

func run(addr, proxy string, noBrowser bool) error {
	for _, address := range []string{addr, proxy} {
		if address == "" {
			continue
		}
		host, _, err := net.SplitHostPort(address)
		if err != nil || host != "127.0.0.1" {
			return fmt.Errorf("portable addresses must use 127.0.0.1:port: %q", address)
		}
	}
	url := "http://" + addr
	if healthy(url) {
		fmt.Println("ARTEX is already running at", url)
		if !noBrowser {
			openBrowser(url)
		}
		return nil
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	root := filepath.Dir(executable)
	if err := os.Chdir(root); err != nil {
		return err
	}
	pgRoot, cleanup, err := postgresRoot(root)
	if err != nil {
		return err
	}
	defer cleanup()
	pgBin := filepath.Join(pgRoot, "runtime", "pgsql", "bin")
	node := filepath.Join(root, "runtime", "node")
	python := filepath.Join(root, "runtime", "python")
	if err := os.Setenv("PATH", strings.Join([]string{node, python, pgBin, os.Getenv("PATH")}, string(os.PathListSeparator))); err != nil {
		return err
	}
	for name, value := range map[string]string{
		"ARTEX_CONFIG":    filepath.Join(root, "config.json"),
		"ARTEX_SKILL_DIR": filepath.Join(root, "skills"),
		"CHROMIUM":        filepath.Join(root, "runtime", "chrome", "chrome.exe"),
	} {
		if err := os.Setenv(name, value); err != nil {
			return err
		}
	}
	if err := os.Unsetenv("ARTEX_PG_DSN"); err != nil {
		return err
	}
	cfg, err := loadConfiguration(root)
	if err != nil {
		return err
	}
	pgData := filepath.Join(pgRoot, "data", "postgres")
	logs := filepath.Join(pgRoot, "data", "logs")
	if err := os.MkdirAll(logs, 0700); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(pgData, "PG_VERSION")); errors.Is(err, os.ErrNotExist) {
		passwordFile, err := os.CreateTemp(filepath.Join(pgRoot, "data"), "initdb-password-")
		if err != nil {
			return err
		}
		passwordPath := passwordFile.Name()
		defer os.Remove(passwordPath)
		if _, err := passwordFile.WriteString(cfg.Database.Password); err != nil {
			passwordFile.Close()
			return err
		}
		if err := passwordFile.Close(); err != nil {
			return err
		}
		fmt.Println("Initializing the local PostgreSQL database...")
		if err := command(filepath.Join(pgBin, "initdb.exe"), "-D", pgData, "-U", cfg.Database.User,
			"--auth-host=scram-sha-256", "--auth-local=scram-sha-256", "--encoding=UTF8", "--locale=C", "--pwfile="+passwordPath).Run(); err != nil {
			return fmt.Errorf("initialize PostgreSQL: %w", err)
		}
		if err := os.Remove(passwordPath); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	pgCtl := filepath.Join(pgBin, "pg_ctl.exe")
	status := exec.Command(pgCtl, "-D", pgData, "status")
	if err := status.Run(); err != nil {
		options := fmt.Sprintf("-h 127.0.0.1 -p %d", cfg.Database.Port)
		if err := command(pgCtl, "-D", pgData, "-l", filepath.Join(logs, "postgresql.log"), "-o", options, "-w", "-t", "30", "start").Run(); err != nil {
			return fmt.Errorf("start PostgreSQL: %w", err)
		}
	}
	defer func() {
		if err := command(pgCtl, "-D", pgData, "-m", "fast", "-w", "-t", "30", "stop").Run(); err != nil {
			fmt.Fprintln(os.Stderr, "[ARTEX] stop PostgreSQL:", err)
		}
	}()
	fmt.Println("ARTEX:", url)
	if !noBrowser {
		go func() {
			for i := 0; i < 45; i++ {
				if healthy(url) {
					openBrowser(url)
					return
				}
				time.Sleep(time.Second)
			}
		}()
	}
	batch := filepath.Join(root, "start-engine.bat")
	cmd := command("cmd.exe")
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: fmt.Sprintf("cmd.exe /d /s /c \"\"%s\" -addr=%s -proxy=%s\"", batch, addr, proxy)}
	return cmd.Run()
}

// PostgreSQL's Windows utilities use ANSI paths when locating their binaries.
// A private junction supplies an ASCII path while keeping data in the package.
func postgresRoot(root string) (string, func(), error) {
	ascii := func(value string) bool {
		for _, r := range value {
			if r > 127 {
				return false
			}
		}
		return true
	}
	if ascii(root) {
		return root, func() {}, nil
	}
	base := filepath.Join(os.Getenv("PUBLIC"), "Documents")
	if !filepath.IsAbs(base) || !ascii(base) {
		return "", nil, errors.New("Windows public Documents directory must have an absolute ASCII path")
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return "", nil, err
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;SY)(A;;FA;;;" + user.User.Sid.String() + ")")
	if err != nil {
		return "", nil, err
	}
	name := make([]byte, 12)
	if _, err := rand.Read(name); err != nil {
		return "", nil, err
	}
	directory := filepath.Join(base, "ARTEX-"+base64.RawURLEncoding.EncodeToString(name))
	directoryPtr, err := windows.UTF16PtrFromString(directory)
	if err != nil {
		return "", nil, err
	}
	attributes := windows.SecurityAttributes{SecurityDescriptor: sd}
	attributes.Length = uint32(unsafe.Sizeof(attributes))
	if err := windows.CreateDirectory(directoryPtr, &attributes); err != nil {
		return "", nil, fmt.Errorf("create PostgreSQL path alias: %w", err)
	}
	link := filepath.Join(directory, "package")
	cleanup := func() {
		os.Remove(link)
		os.Remove(directory)
	}
	cmd := exec.Command("cmd.exe")
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: `cmd.exe /d /c mklink /J "%ARTEX_JUNCTION_LINK%" "%ARTEX_JUNCTION_SOURCE%"`}
	cmd.Env = append(os.Environ(), "ARTEX_JUNCTION_LINK="+link, "ARTEX_JUNCTION_SOURCE="+root)
	if output, err := cmd.CombinedOutput(); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("create PostgreSQL path alias: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return link, cleanup, nil
}

func loadConfiguration(root string) (configuration, error) {
	var cfg configuration
	path := filepath.Join(root, "config.json")
	data, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(data, &cfg); err != nil {
			return cfg, fmt.Errorf("read config.json: %w", err)
		}
		if cfg.Database.Host != "127.0.0.1" || cfg.Database.Port < 1 || cfg.Database.Port > 65535 || cfg.Database.User == "" {
			return cfg, errors.New("portable config.json requires a local database at 127.0.0.1 with a valid port and user")
		}
		return cfg, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return cfg, err
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return cfg, err
	}
	cfg.Database.Port = listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	password := make([]byte, 24)
	if _, err := rand.Read(password); err != nil {
		return cfg, err
	}
	cfg.Database.Host = "127.0.0.1"
	cfg.Database.User = "artex"
	cfg.Database.Password = base64.RawURLEncoding.EncodeToString(password)
	cfg.Database.DBName = "artex"
	cfg.Database.SSLMode = "disable"
	cfg.SkillDir = "skills"
	data, err = json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return cfg, err
	}
	return cfg, os.WriteFile(path, append(data, '\n'), 0600)
}

func command(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd
}

func healthy(url string) bool {
	client := http.Client{Timeout: time.Second}
	response, err := client.Get(url + "/api/health")
	if err != nil {
		return false
	}
	defer response.Body.Close()
	var body struct {
		OK      bool   `json:"ok"`
		Service string `json:"service"`
	}
	return response.StatusCode == http.StatusOK && json.NewDecoder(response.Body).Decode(&body) == nil && body.OK && body.Service == "artex"
}

func openBrowser(url string) {
	if err := exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", url).Run(); err != nil {
		fmt.Fprintln(os.Stderr, "Open", url, "in your browser.")
	}
}
