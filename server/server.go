/*
 * Copyright (c) SAS Institute Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"os"
	"runtime"

	"gerrit-pdt.unx.sas.com/tools/relic.git/config"
)

type Server struct {
	Config   *config.Config
	ErrorLog *log.Logger
}

func (s *Server) callHandler(request *http.Request) (response Response, err error) {
	defer func() {
		if caught := recover(); caught != nil {
			const size = 64 << 10
			buf := make([]byte, size)
			buf = buf[:runtime.Stack(buf, false)]
			s.Logf("Unhandled exception from client %s: %s\n%s\n", GetClientIP(request), caught, buf)
			response = ErrorResponse(http.StatusInternalServerError)
			err = nil
		}
	}()
	ctx := request.Context()
	ctx, errResponse := s.getUserRoles(ctx, request)
	if errResponse != nil {
		return errResponse, nil
	}
	request = request.WithContext(ctx)
	switch request.URL.Path {
	case "/":
		return s.serveHome(request)
	case "/list_keys":
		return s.serveListKeys(request)
	case "/sign":
		return s.serveSign(request)
	default:
		return ErrorResponse(http.StatusNotFound), nil
	}
}

func (s *Server) getUserRoles(ctx context.Context, request *http.Request) (context.Context, Response) {
	if request.TLS == nil {
		return nil, StringResponse(http.StatusBadRequest, "Retry request using TLS")
	}
	if len(request.TLS.PeerCertificates) == 0 {
		return nil, StringResponse(http.StatusBadRequest, "Invalid client certificate")
	}
	cert := request.TLS.PeerCertificates[0]
	digest := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
	encoded := hex.EncodeToString(digest[:])
	client, ok := s.Config.Clients[encoded]
	if !ok {
		s.Logf("Denied access to unknown client %s with fingerprint %s\n", GetClientIP(request), encoded)
		return nil, AccessDeniedResponse
	}
	ctx = context.WithValue(ctx, ctxClientName, client.Nickname)
	ctx = context.WithValue(ctx, ctxRoles, client.Roles)
	return ctx, nil
}

func (s *Server) CheckKeyAccess(request *http.Request, keyName string) *config.KeyConfig {
	keyConf, err := s.Config.GetKey(keyName)
	if err != nil {
		return nil
	}
	clientRoles := GetClientRoles(request)
	for _, keyRole := range keyConf.Roles {
		for _, clientRole := range clientRoles {
			if keyRole == clientRole {
				return keyConf
			}
		}
	}
	return nil
}

func (s *Server) Logf(format string, args ...interface{}) {
	s.ErrorLog.Output(2, fmt.Sprintf(format, args...))
}

func (s *Server) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	response, err := s.callHandler(request)
	if err != nil {
		s.Logf("Unhandled exception from client %s: %s\n", GetClientIP(request), err)
		response = ErrorResponse(http.StatusInternalServerError)
	}
	defer response.Close()
	response.Write(writer)
}

func New(config *config.Config) (*Server, error) {
	var logger *log.Logger
	if config.Server.LogFile != "" {
		f, err := os.OpenFile(config.Server.LogFile, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0666)
		if err != nil {
			return nil, fmt.Errorf("failed to open logfile: %s", err)
		}
		logger = log.New(f, "", log.Ldate|log.Ltime|log.Lmicroseconds)
	} else {
		logger = log.New(os.Stderr, "", 0)
	}
	return &Server{Config: config, ErrorLog: logger}, nil
}