package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/jaypipes/ghw"
	cli "github.com/urfave/cli/v3" // imports as package "cli"
)

func main() {
	if _, ok := os.LookupEnv("EGPU_HOTPLUG_DEBUG"); !ok {
		os.Setenv("GHW_DISABLE_WARNINGS", "1")
	}
	app := &cli.Command{
		EnableShellCompletion: true,
		Name:                  "egpu-hotplug",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "gpu",
				Usage:   "string to match GPU name",
				Aliases: []string{"g"},
				Value:   "Ellesmere",
			},
			&cli.BoolFlag{
				Name:    "force",
				Usage:   "force unbind",
				Aliases: []string{"f"},
				Value:   false,
			},
			&cli.StringFlag{
				Name:    "tb",
				Aliases: []string{"t"},
				Usage:   "tb device",
				Value:   "c7010000-0052-540e-03af-bfd8ce248908",
			},
		},
		Commands: []*cli.Command{
			bind(),
			unbind(),
		},
		DefaultCommand: "help",
	}

	if err := app.Run(context.Background(), os.Args); err != nil {
		log.Fatal(err)
	}
}

func unbind() *cli.Command {
	return &cli.Command{
		Name:  "unbind",
		Usage: "unbind eGPU",
		Action: func(c context.Context, cli *cli.Command) error {
			gpu := cli.String("gpu")
			exist, err := checkGPU(gpu)
			if err != nil {
				return err
			}
			if !exist {
				fmt.Printf("GPU '%s' not found. Nothing to do.\n", gpu)
				return nil
			}

			gpuAddr, err := getDeviceAddress(gpu)
			if err != nil {
				return err
			}

			if !cli.Bool("force") && !isFree(gpuAddr) {
				return fmt.Errorf("GPU '%s' is not free", gpu)
			}

			audioAddr := strings.Replace(gpuAddr, "00.0", "00.1", 1)
			err = writeSysFs(fmt.Sprintf("/sys/bus/pci/devices/%s/driver/unbind", gpuAddr), gpuAddr)
			if err != nil {
				return err
			}
			err = writeSysFs(fmt.Sprintf("/sys/bus/pci/devices/%s/driver/unbind", audioAddr), audioAddr)
			if err != nil {
				return err
			}
			fmt.Printf("GPU '%s' unbound, please remove the cable.\n", gpu)
			return nil
		},
	}
}

func isFree(addr string) bool {
	// execute `fuser -v /dev/dri/by-path/pci-addr`
	cmd := exec.Command("fuser", "-v", fmt.Sprintf("/dev/dri/by-path/pci-%s-render", addr))
	out, err := cmd.Output()
	if err != nil {
		err, ok := err.(*exec.ExitError)
		if ok && err.ExitCode() == 1 && len(err.Stderr) == 0 {
			return true
		} else {
			fmt.Printf("GPU '%s' failed to check free %+v\n", addr, err)
		}
		return false
	}
	return len(out) == 0
}

func getDeviceAddress(name string) (string, error) {
	gpus, err := ghw.GPU()
	if err != nil {
		return "", err
	}
	for _, gpu := range gpus.GraphicsCards {
		if gpu.DeviceInfo.Driver != "amdgpu" {
			continue
		}
		if strings.Contains(gpu.DeviceInfo.Product.Name, name) {
			return gpu.Address, nil
		}
	}
	return "", fmt.Errorf("GPU '%s' not found", name)
}

func bind() *cli.Command {
	return &cli.Command{
		Name:  "bind",
		Usage: "bind eGPU",
		Action: func(c context.Context, cli *cli.Command) error {
			devicePort := cli.String("tb")
			pciPath, connected := dockConnected(devicePort)
			if !connected {
				return fmt.Errorf("Dock not connected")
			}
			path := findRescanDevice(pciPath)
			path = findRescanDevice(path) // strange but we need to have parent of the device for rescan
			if path == "" {
				path = "/sys/bus/pci" // rescan all?
			}
			fmt.Printf("Rescan path: %s/rescan\n", path)

			err := writeSysFs(fmt.Sprintf("%s/rescan", path), "1")
			if err != nil {
				return err
			}
			gpu := cli.String("gpu")
			exists, err := checkGPU(gpu)
			if err != nil {
				return err
			}
			if !exists {
				return fmt.Errorf("GPU '%s' not found", gpu)
			}
			fmt.Printf("GPU '%s' found\n", gpu)
			return nil
		},
	}
}

func checkGPU(name string) (bool, error) {
	for i := 0; i < 10; i++ {
		gpus, err := ghw.GPU()
		if err != nil {
			if i > 0 {
				fmt.Println()
			}
			return false, err
		}
		for _, gpu := range gpus.GraphicsCards {
			if gpu.DeviceInfo.Driver != "amdgpu" {
				continue
			}
			if strings.Contains(gpu.DeviceInfo.Product.Name, name) {
				if i > 0 {
					fmt.Println()
				}
				return true, nil
			}
		}
		fmt.Print(".")
		time.Sleep(1 * time.Second)
	}
	fmt.Println()
	return false, nil
}

func writeSysFs(path, value string) error {
	data := []byte(value)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 644)
	if err != nil {
		return err
	}
	n, err := f.Write(data)
	if err == nil && n < len(data) {
		err = io.ErrShortWrite
	}
	if err1 := f.Close(); err == nil {
		err = err1
	}
	return err
}

func findRescanDevice(path string) string {
	if path == "/" {
		return ""
	}
	parent := filepath.Dir(path)
	rescanPath := fmt.Sprintf("%s/rescan", parent)
	if _, err := os.Stat(rescanPath); err == nil {
		return parent
	}
	return findRescanDevice(parent)
}

func dockConnected(thunderboltDevice string) (devicePciPath string, connected bool) {
	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Failed to connect to system bus:", err)
		return "", false
	}
	defer conn.Close()

	var tbDest string
	obj := conn.Object("org.freedesktop.DBus", "/org/freedesktop/DBus")
	err = obj.Call("org.freedesktop.DBus.GetNameOwner", 0, "org.freedesktop.bolt").Store(&tbDest)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Failed to call GetNameOwner for tb interface ", err)
		return "", false
	}
	var device string
	obj = conn.Object(tbDest, "/org/freedesktop/bolt")
	err = obj.Call("org.freedesktop.bolt1.Manager.DeviceByUid", 0, thunderboltDevice).Store(&device)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Failed to call org.freedesktop.bolt1.Manager.DeviceByUid:", err)
		return "", false
	}

	var data map[string]dbus.Variant
	obj = conn.Object(tbDest, dbus.ObjectPath(device))
	err = obj.Call("org.freedesktop.DBus.Properties.GetAll", 0, "org.freedesktop.bolt1.Device").Store(&data)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Failed to call org.freedesktop.DBus.Properties.GetAll:", err)
		return "", false
	}
	return data["SysfsPath"].Value().(string), data["Status"].Value().(string) == "authorized"
}
