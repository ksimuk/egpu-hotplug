package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"time"

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
				Name:  "gpu",
				Usage: "string to match GPU name",
				Value: "580",
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
			err := writeSysFs("/sys/bus/pci/rescan", "1")
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
