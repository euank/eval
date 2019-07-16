package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"time"
)

type evalRequest struct {
	Key      string `json:"key"`
	Env      string `json:"env"`
	Contents string `json:"contents"`
}

type evalResp struct {
	Response *RunResponse `json:"response"`
}

func main() {
	psk := os.Getenv("EVAL_PSK")
	if psk == "" {
		log.Fatalf("must set the EVAL_PSK environment variable to a preshared secret")
	}

	http.ListenAndServe(":8080", http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		var e evalRequest
		err := json.NewDecoder(req.Body).Decode(&e)
		if err != nil {
			writeError(rw, 400, "could not decode request as valid json")
			return
		}
		if e.Key != psk {
			writeError(rw, 401, "permission denied; invalid key")
			return
		}

		ctx, cancel := context.WithTimeout(req.Context(), 20*time.Second)
		defer cancel()
		env, err := startEnv(ctx, e.Env)
		if err != nil {
			// TODO: distinguish 4xx/5xx here
			writeError(rw, 400, "unable to start environment: "+err.Error())
			return
		}
		defer func() {
			go env.Cleanup()
		}()

		resp, err := env.Run(ctx, e.Contents)
		if err != nil {
			// TODO: distinguish 4xx/5xx here
			writeError(rw, 400, "error running code: "+err.Error())
			return
		}
		json.NewEncoder(rw).Encode(&evalResp{
			Response: resp,
		})
	}))
}

func writeError(rw http.ResponseWriter, status int, body string) {
	rw.WriteHeader(status)
	rw.Write([]byte(body))
}
