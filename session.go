package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/inconshreveable/log15"
)

type Env interface {
	Run(context.Context, string) (*RunResponse, error)
	Cleanup()
}

type RunResponse struct {
	Stdout  string `json:"stdout"`
	Stderr  string `json:"stderr"`
	Timeout bool   `json:"timeout"`
}

func startEnv(ctx context.Context, name string) (Env, error) {
	switch name {
	case "python", "py":
		return newPythonEnv(ctx)
	default:
		return nil, fmt.Errorf("invalid environment: %v", name)
	}
}

const pythonImage = "euank/python:3.6"

type pythonEnv struct {
	dockerID string
}

func newPythonEnv(ctx context.Context) (Env, error) {
	cli, err := client.NewClientWithOpts(client.WithAPIVersionNegotiation())
	if err != nil {
		log15.Error("docker client error", "err", err)
		return nil, fmt.Errorf("error connecting to docker to eval python")
	}

	pidsLimit := int64(100)
	resp, err := cli.ContainerCreate(
		ctx,
		&container.Config{
			OpenStdin:    true,
			StdinOnce:    true,
			AttachStdin:  true,
			AttachStdout: true,
			AttachStderr: true,
			Image:        pythonImage,
		},
		&container.HostConfig{
			//AutoRemove: true,
			DNS: []string{"8.8.8.8"},
			Resources: container.Resources{
				Memory:    50 * 1024 * 1024,
				CPUPeriod: 100000,
				CPUQuota:  50000,
				PidsLimit: &pidsLimit,
			},
		},
		&network.NetworkingConfig{},
		"",
	)
	if err != nil {
		log15.Error("docker create error", "err", err)
		return nil, fmt.Errorf("error creating python container: %v", err)
	}
	env := &pythonEnv{dockerID: resp.ID}

	err = cli.ContainerStart(
		ctx,
		resp.ID,
		types.ContainerStartOptions{},
	)
	if err != nil {
		log15.Error("docker start error", "err", err)
		return nil, fmt.Errorf("error starting python container: %v", err)
	}
	return env, nil
}

func (e *pythonEnv) Run(ctx context.Context, body string) (*RunResponse, error) {
	cli, err := client.NewClientWithOpts(client.WithAPIVersionNegotiation())
	if err != nil {
		log15.Error("docker client error", "err", err)
		return nil, fmt.Errorf("error getting docker client to run command")
	}

	hj, err := cli.ContainerAttach(ctx, e.dockerID, types.ContainerAttachOptions{
		Stream: true,
		Stdin:  true,
		Stdout: true,
		Stderr: true,
	})
	if err != nil {
		log15.Error("docker attach error", "err", err)
		return nil, fmt.Errorf("error attaching to container")
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	copyErr := make(chan error)
	go func() {
		_, err := stdcopy.StdCopy(&stdout, &stderr, hj.Reader)
		copyErr <- err
	}()
	if _, err := io.Copy(hj.Conn, strings.NewReader(body)); err != nil {
		return nil, fmt.Errorf("error writing program to container: %v", err)
	}
	if err := hj.CloseWrite(); err != nil {
		log15.Error("could not close writer", "err", err)
	}
	log15.Debug("copied in body; waiting for response", "body", body)

	timeout := false
	select {
	case err = <-copyErr:
		if err != nil {
			return nil, err
		}
	case <-ctx.Done():
		hj.Close()
		<-copyErr
		timeout = true
	}

	return &RunResponse{
		Timeout: timeout,
		Stdout:  stdout.String(),
		Stderr:  stderr.String(),
	}, nil
}

func (e *pythonEnv) Cleanup() {
	cli, err := client.NewClientWithOpts(client.WithAPIVersionNegotiation())
	if err != nil {
		log15.Error("docker client error", "err", err)
		return
	}
	stopTimeout := 5 * time.Second
	err = cli.ContainerStop(context.TODO(), e.dockerID, &stopTimeout)
	if err != nil {
		log15.Error("docker client stop error", "err", err)
	}
}
