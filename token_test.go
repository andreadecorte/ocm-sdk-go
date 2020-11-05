/*
Copyright (c) 2019 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// This file contains tests for the methods that request tokens.

package sdk

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	. "github.com/onsi/ginkgo" // nolint
	. "github.com/onsi/gomega" // nolint

	"github.com/onsi/gomega/ghttp"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

var _ = Describe("Tokens", func() {
	// Servers used during the tests:
	var oidServer *ghttp.Server
	var apiServer *ghttp.Server

	// Metrics subsystem - value doesn't matter but configuring it enables
	// prometheus exporting, exercising the counter increment functionality
	// (e.g. will catch inconsistent labels).
	metrics := "test_subsystem"

	BeforeEach(func() {
		// Create the servers:
		oidServer = MakeServer()
		apiServer = MakeServer()
	})

	AfterEach(func() {
		// Stop the servers:
		oidServer.Close()
		apiServer.Close()
	})

	Describe("Refresh grant", func() {
		It("Returns the access token generated by the server", func() {
			// Generate the tokens:
			accessToken := DefaultToken("Bearer", 5*time.Minute)
			refreshToken := DefaultToken("Refresh", 10*time.Hour)

			// Configure the server:
			oidServer.AppendHandlers(
				ghttp.CombineHandlers(
					VerifyRefreshGrant(refreshToken),
					RespondWithTokens(accessToken, refreshToken),
				),
			)

			// Create the connection:
			connection, err := NewConnectionBuilder().
				Logger(logger).
				Metrics(metrics).
				TokenURL(oidServer.URL()).
				URL(apiServer.URL()).
				Tokens(refreshToken).
				Build()
			Expect(err).ToNot(HaveOccurred())
			defer connection.Close()

			// Get the tokens:
			returnedAccess, returnedRefresh, err := connection.Tokens()
			Expect(err).ToNot(HaveOccurred())
			Expect(returnedAccess).To(Equal(accessToken))
			Expect(returnedRefresh).To(Equal(refreshToken))
		})

		It("Sends the token request the first time only", func() {
			// Generate the tokens:
			accessToken := DefaultToken("Bearer", 5*time.Minute)
			refreshToken := DefaultToken("Refresh", 10*time.Hour)

			// Configure the server:
			oidServer.AppendHandlers(
				ghttp.CombineHandlers(
					VerifyRefreshGrant(refreshToken),
					RespondWithTokens(accessToken, refreshToken),
				),
			)

			// Create the connection:
			connection, err := NewConnectionBuilder().
				Logger(logger).
				Metrics(metrics).
				TokenURL(oidServer.URL()).
				URL(apiServer.URL()).
				Tokens(refreshToken).
				Build()
			Expect(err).ToNot(HaveOccurred())
			defer connection.Close()

			// Get the tokens the first time:
			firstAccess, firstRefresh, err := connection.Tokens()
			Expect(err).ToNot(HaveOccurred())

			// Get the tones the second time:
			secondAccess, secondRefresh, err := connection.Tokens()
			Expect(err).ToNot(HaveOccurred())
			Expect(firstAccess).To(Equal(secondAccess))
			Expect(firstRefresh).To(Equal(secondRefresh))
		})

		It("Refreshes the access token request if it is expired", func() {
			// Generate the tokens:
			expiredAccess := DefaultToken("Bearer", -5*time.Minute)
			validAccess := DefaultToken("Bearer", 5*time.Minute)
			refreshToken := DefaultToken("Refresh", 10*time.Hour)

			// Configure the server:
			oidServer.AppendHandlers(
				ghttp.CombineHandlers(
					VerifyRefreshGrant(refreshToken),
					RespondWithTokens(validAccess, refreshToken),
				),
			)

			// Create the connection:
			connection, err := NewConnectionBuilder().
				Logger(logger).
				Metrics(metrics).
				TokenURL(oidServer.URL()).
				URL(apiServer.URL()).
				Tokens(expiredAccess, refreshToken).
				Build()
			Expect(err).ToNot(HaveOccurred())
			defer connection.Close()

			// Get the tokens:
			returnedAccess, _, err := connection.Tokens()
			Expect(err).ToNot(HaveOccurred())
			Expect(returnedAccess).To(Equal(validAccess))
		})

		It("Refreshes the access token if it expires in less than one minute", func() {
			// Generate the tokens:
			firstAccess := DefaultToken("Bearer", 50*time.Second)
			secondAccess := DefaultToken("Bearer", 5*time.Minute)
			refreshToken := DefaultToken("Refresh", 10*time.Hour)

			// Configure the server:
			oidServer.AppendHandlers(
				ghttp.CombineHandlers(
					VerifyRefreshGrant(refreshToken),
					RespondWithTokens(secondAccess, refreshToken),
				),
			)

			// Create the connection:
			connection, err := NewConnectionBuilder().
				Logger(logger).
				Metrics(metrics).
				TokenURL(oidServer.URL()).
				URL(apiServer.URL()).
				Tokens(firstAccess, refreshToken).
				Build()
			Expect(err).ToNot(HaveOccurred())
			defer connection.Close()

			// Get the tokens:
			returnedAccess, _, err := connection.Tokens()
			Expect(err).ToNot(HaveOccurred())
			Expect(returnedAccess).To(Equal(secondAccess))
		})

		It("Refreshes the access token if it expires in less than specified expiry period", func() {
			// Ask for a token valid for at least 10 minutes
			expiresIn := 10 * time.Minute

			// Generate the tokens:
			firstAccess := DefaultToken("Bearer", 9*time.Minute)
			secondAccess := DefaultToken("Bearer", 20*time.Minute)
			refreshToken := DefaultToken("Refresh", 10*time.Hour)

			// Configure the server:
			oidServer.AppendHandlers(
				ghttp.CombineHandlers(
					VerifyRefreshGrant(refreshToken),
					RespondWithTokens(secondAccess, refreshToken),
				),
			)

			// Create the connection:
			connection, err := NewConnectionBuilder().
				Logger(logger).
				Metrics(metrics).
				TokenURL(oidServer.URL()).
				URL(apiServer.URL()).
				Tokens(firstAccess, refreshToken).
				Build()
			Expect(err).ToNot(HaveOccurred())
			defer connection.Close()

			// Get the tokens:
			returnedAccess, _, err := connection.Tokens(expiresIn)
			Expect(err).ToNot(HaveOccurred())
			Expect(returnedAccess).To(Equal(secondAccess))
		})

		It("Fails if the access token is expired and there is no refresh token", func() {
			// Generate the tokens:
			accessToken := DefaultToken("Bearer", -5*time.Second)

			// Create the connection:
			connection, err := NewConnectionBuilder().
				Logger(logger).
				Metrics(metrics).
				TokenURL(oidServer.URL()).
				URL(apiServer.URL()).
				Tokens(accessToken).
				Build()
			Expect(err).ToNot(HaveOccurred())
			defer connection.Close()

			// Get the tokens:
			_, _, err = connection.Tokens()
			Expect(err).To(HaveOccurred())
		})

		It("Succeeds if access token expires soon and there is no refresh token", func() {
			// Generate the tokens:
			accessToken := DefaultToken("Bearer", 1*time.Second)

			// Create the connection:
			connection, err := NewConnectionBuilder().
				Logger(logger).
				Metrics(metrics).
				TokenURL(oidServer.URL()).
				URL(apiServer.URL()).
				Tokens(accessToken).
				Build()
			Expect(err).ToNot(HaveOccurred())
			defer connection.Close()

			// Get the tokens:
			_, _, err = connection.Tokens()
			Expect(err).ToNot(HaveOccurred())
		})

		It("Fails if the refresh token is expired", func() {
			// Generate the tokens:
			refreshToken := DefaultToken("Refresh", -5*time.Second)

			// Create the connection:
			connection, err := NewConnectionBuilder().
				Logger(logger).
				Metrics(metrics).
				TokenURL(oidServer.URL()).
				URL(apiServer.URL()).
				Tokens(refreshToken).
				Build()
			Expect(err).ToNot(HaveOccurred())
			defer connection.Close()

			// Get the tokens:
			_, _, err = connection.Tokens()
			Expect(err).To(HaveOccurred())
		})

		When("The server doesn't return JSON content type", func() {
			It("Adds complete content to error message if it is short", func() {
				// Generate the refresh token:
				refreshToken := DefaultToken("Refresh", 10*time.Hour)

				// Configure the server:
				for i := 0; i < 100; i++ { // there are going to be several retries
					oidServer.AppendHandlers(
						ghttp.RespondWith(
							http.StatusServiceUnavailable,
							`Service unavailable`,
							http.Header{
								"Content-Type": []string{
									"text/plain",
								},
							},
						),
					)
				}

				// Create the connection:
				connection, err := NewConnectionBuilder().
					Logger(logger).
					Metrics(metrics).
					TokenURL(oidServer.URL()).
					URL(apiServer.URL()).
					Tokens(refreshToken).
					Build()
				Expect(err).ToNot(HaveOccurred())
				defer connection.Close()

				// Try to get the access token:
				ctx, _ := context.WithTimeout(context.Background(), 100*time.Millisecond)
				_, _, err = connection.TokensContext(ctx)
				Expect(err).To(HaveOccurred())
				message := err.Error()
				Expect(message).To(ContainSubstring("text/plain"))
				Expect(message).To(ContainSubstring("Service unavailable"))
			})

			It("Adds summary of content if it is too long", func() {
				// Generate the refresh token:
				refreshToken := DefaultToken("Refresh", 10*time.Hour)

				// Calculate a long message:
				content := fmt.Sprintf("Ver%s long", strings.Repeat("y", 1000))

				// Configure the server:
				oidServer.AppendHandlers(
					ghttp.RespondWith(
						http.StatusBadRequest,
						content,
						http.Header{
							"Content-Type": []string{
								"text/plain",
							},
						},
					),
				)

				// Create the connection:
				connection, err := NewConnectionBuilder().
					Logger(logger).
					Metrics(metrics).
					TokenURL(oidServer.URL()).
					URL(apiServer.URL()).
					Tokens(refreshToken).
					Build()
				Expect(err).ToNot(HaveOccurred())
				defer connection.Close()

				// Try to get the access token:
				_, _, err = connection.Tokens()
				Expect(err).To(HaveOccurred())
				message := err.Error()
				Expect(message).To(ContainSubstring("text/plain"))
				Expect(message).To(ContainSubstring("Veryyyyyy"))
				Expect(message).To(ContainSubstring("..."))
			})
		})

		It("Honors cookies", func() {
			// Generate the tokens:
			expiredAccess := DefaultToken("Bearer", -5*time.Minute)
			validAccess := DefaultToken("Bearer", 5*time.Minute)
			refreshToken := DefaultToken("Refresh", 10*time.Hour)

			// Configure the server:
			oidServer.AppendHandlers(
				ghttp.CombineHandlers(
					RespondWithCookie("mycookie", "myvalue"),
					RespondWithTokens(expiredAccess, refreshToken),
				),
				ghttp.CombineHandlers(
					VerifyCookie("mycookie", "myvalue"),
					RespondWithTokens(validAccess, refreshToken),
				),
			)

			// Create the connection:
			connection, err := NewConnectionBuilder().
				Logger(logger).
				Metrics(metrics).
				TokenURL(oidServer.URL()).
				URL(apiServer.URL()).
				Tokens(expiredAccess, refreshToken).
				Build()
			Expect(err).ToNot(HaveOccurred())
			defer connection.Close()

			// Request the tokens the first time. This will return an expired access
			// token and a valid refresh token.
			_, _, err = connection.Tokens()
			Expect(err).ToNot(HaveOccurred())

			// Request the tokens a second time, therefore forcing a refresh grant which
			// should honor the cookies returned in the first attempt:
			_, _, err = connection.Tokens()
			Expect(err).ToNot(HaveOccurred())
		})
	})

	Describe("Password grant", func() {
		It("Returns the access and refresh tokens generated by the server", func() {
			// Generate the tokens:
			accessToken := DefaultToken("Bearer", 5*time.Minute)
			refreshToken := DefaultToken("Refresh", 10*time.Hour)

			// Configure the server:
			oidServer.AppendHandlers(
				ghttp.CombineHandlers(
					VerifyPasswordGrant("myuser", "mypassword"),
					RespondWithTokens(accessToken, refreshToken),
				),
			)

			// Create the connection:
			connection, err := NewConnectionBuilder().
				Logger(logger).
				Metrics(metrics).
				TokenURL(oidServer.URL()).
				URL(apiServer.URL()).
				User("myuser", "mypassword").
				Build()
			Expect(err).ToNot(HaveOccurred())
			defer connection.Close()

			// Get the tokens:
			returnedAccess, returnedRefresh, err := connection.Tokens()
			Expect(err).ToNot(HaveOccurred())
			Expect(returnedAccess).To(Equal(accessToken))
			Expect(returnedRefresh).To(Equal(refreshToken))
		})

		It("Refreshes access token", func() {
			// Generate the tokens:
			expiredAccess := DefaultToken("Bearer", -5*time.Second)
			validAccess := DefaultToken("Bearer", 5*time.Minute)
			refreshToken := DefaultToken("Refresh", 10*time.Hour)

			// Configure the server:
			oidServer.AppendHandlers(
				ghttp.CombineHandlers(
					VerifyPasswordGrant("myuser", "mypassword"),
					RespondWithTokens(expiredAccess, refreshToken),
				),
				ghttp.CombineHandlers(
					VerifyRefreshGrant(refreshToken),
					RespondWithTokens(validAccess, refreshToken),
				),
			)

			// Create the connection:
			connection, err := NewConnectionBuilder().
				Logger(logger).
				Metrics(metrics).
				TokenURL(oidServer.URL()).
				URL(apiServer.URL()).
				User("myuser", "mypassword").
				Build()
			Expect(err).ToNot(HaveOccurred())
			defer connection.Close()

			// Get the tokens the first time:
			firstAccess, _, err := connection.Tokens()
			Expect(err).ToNot(HaveOccurred())
			Expect(firstAccess).To(Equal(expiredAccess))

			// Get the tokens the second time:
			secondAccess, _, err := connection.Tokens()
			Expect(err).ToNot(HaveOccurred())
			Expect(secondAccess).To(Equal(validAccess))
		})

		It("Requests a new refresh token when it expires", func() {
			// Generate the tokens:
			expiredAccess := DefaultToken("Bearer", -5*time.Second)
			expiredRefresh := DefaultToken("Refresh", -15*time.Second)
			validAccess := DefaultToken("Bearer", 5*time.Minute)
			validRefresh := DefaultToken("Refresh", 10*time.Hour)

			// Configure the server:
			oidServer.AppendHandlers(
				ghttp.CombineHandlers(
					VerifyPasswordGrant("myuser", "mypassword"),
					RespondWithTokens(expiredAccess, expiredRefresh),
				),
				ghttp.CombineHandlers(
					VerifyPasswordGrant("myuser", "mypassword"),
					RespondWithTokens(validAccess, validRefresh),
				),
			)

			// Create the connection:
			connection, err := NewConnectionBuilder().
				Logger(logger).
				Metrics(metrics).
				TokenURL(oidServer.URL()).
				URL(apiServer.URL()).
				User("myuser", "mypassword").
				Build()
			Expect(err).ToNot(HaveOccurred())
			defer connection.Close()

			// Get the tokens the first time:
			_, firstRefresh, err := connection.Tokens()
			Expect(err).ToNot(HaveOccurred())
			Expect(firstRefresh).To(Equal(expiredRefresh))

			// Get the tokens the second time:
			_, secondRefresh, err := connection.Tokens()
			Expect(err).ToNot(HaveOccurred())
			Expect(secondRefresh).To(Equal(validRefresh))
		})

		It("Requests a new refresh token when expires in less than ten seconds", func() {
			// Generate the tokens:
			expiredAccess := DefaultToken("Bearer", -5*time.Second)
			expiredRefresh := DefaultToken("Refresh", 5*time.Second)
			validAccess := DefaultToken("Bearer", 5*time.Minute)
			validRefresh := DefaultToken("Refresh", 10*time.Hour)

			// Configure the server:
			oidServer.AppendHandlers(
				ghttp.CombineHandlers(
					VerifyPasswordGrant("myuser", "mypassword"),
					RespondWithTokens(expiredAccess, expiredRefresh),
				),
				ghttp.CombineHandlers(
					VerifyPasswordGrant("myuser", "mypassword"),
					RespondWithTokens(validAccess, validRefresh),
				),
			)

			// Create the connection:
			connection, err := NewConnectionBuilder().
				Logger(logger).
				Metrics(metrics).
				TokenURL(oidServer.URL()).
				URL(apiServer.URL()).
				User("myuser", "mypassword").
				Build()
			Expect(err).ToNot(HaveOccurred())
			defer connection.Close()

			// Get the tokens the first time:
			_, firstRefresh, err := connection.Tokens()
			Expect(err).ToNot(HaveOccurred())
			Expect(firstRefresh).To(Equal(expiredRefresh))

			// Get the tokens the second time:
			_, secondRefresh, err := connection.Tokens()
			Expect(err).ToNot(HaveOccurred())
			Expect(secondRefresh).To(Equal(validRefresh))
		})

		It("Fails with wrong user name", func() {
			// Configure the server:
			oidServer.AppendHandlers(
				ghttp.CombineHandlers(
					VerifyPasswordGrant("baduser", "mypassword"),
					RespondWithError("bad_user", "Bad user"),
				),
			)

			// Create the connection:
			connection, err := NewConnectionBuilder().
				Logger(logger).
				Metrics(metrics).
				TokenURL(oidServer.URL()).
				URL(apiServer.URL()).
				User("baduser", "mypassword").
				Build()
			Expect(err).ToNot(HaveOccurred())
			defer connection.Close()

			// Get the tokens:
			_, _, err = connection.Tokens()
			Expect(err).To(HaveOccurred())
		})

		It("Fails with wrong password", func() {
			// Configure the server:
			oidServer.AppendHandlers(
				ghttp.CombineHandlers(
					VerifyPasswordGrant("myuser", "badpassword"),
					RespondWithError("bad_password", "Bad password"),
				),
			)

			// Create the connection:
			connection, err := NewConnectionBuilder().
				Logger(logger).
				Metrics(metrics).
				TokenURL(oidServer.URL()).
				URL(apiServer.URL()).
				User("myuser", "badpassword").
				Build()
			Expect(err).ToNot(HaveOccurred())
			defer connection.Close()

			// Get the tokens:
			_, _, err = connection.Tokens()
			Expect(err).To(HaveOccurred())
		})

		It("Honors cookies", func() {
			// Generate the tokens:
			expiredAccess := DefaultToken("Bearer", -5*time.Minute)
			validAccess := DefaultToken("Bearer", 5*time.Minute)
			refreshToken := DefaultToken("Refresh", 10*time.Hour)

			// Configure the server:
			oidServer.AppendHandlers(
				ghttp.CombineHandlers(
					RespondWithCookie("mycookie", "myvalue"),
					RespondWithTokens(expiredAccess, refreshToken),
				),
				ghttp.CombineHandlers(
					VerifyCookie("mycookie", "myvalue"),
					RespondWithTokens(validAccess, refreshToken),
				),
			)

			// Create the connection:
			connection, err := NewConnectionBuilder().
				Logger(logger).
				Metrics(metrics).
				TokenURL(oidServer.URL()).
				URL(apiServer.URL()).
				User("myuser", "mypassword").
				Build()
			Expect(err).ToNot(HaveOccurred())
			defer connection.Close()

			// Request the tokens the first time. This will return an expired access
			// token and a valid refresh token.
			_, _, err = connection.Tokens()
			Expect(err).ToNot(HaveOccurred())

			// Request the tokens a second time, therefore forcing a refresh grant which
			// should honor the cookies returned in the first attempt:
			_, _, err = connection.Tokens()
			Expect(err).ToNot(HaveOccurred())
		})
	})

	When("Only the access token is provided", func() {
		It("Returns the access token if it hasn't expired", func() {
			// Generate the token:
			accessToken := DefaultToken("Bearer", 5*time.Minute)

			// Create the connection:
			connection, err := NewConnectionBuilder().
				Logger(logger).
				Metrics(metrics).
				TokenURL(oidServer.URL()).
				URL(apiServer.URL()).
				Tokens(accessToken).
				Build()
			Expect(err).ToNot(HaveOccurred())
			defer connection.Close()

			// Get the tokens:
			returnedAccess, returnedRefresh, err := connection.Tokens()
			Expect(err).ToNot(HaveOccurred())
			Expect(returnedAccess).To(Equal(accessToken))
			Expect(returnedRefresh).To(BeEmpty())
		})

		It("Returns an error if the access token has expired", func() {
			// Generate the token:
			accessToken := DefaultToken("Bearer", -5*time.Minute)

			// Create the connection:
			connection, err := NewConnectionBuilder().
				Logger(logger).
				Metrics(metrics).
				TokenURL(oidServer.URL()).
				URL(apiServer.URL()).
				Tokens(accessToken).
				Build()
			Expect(err).ToNot(HaveOccurred())
			defer connection.Close()

			// Get the tokens:
			returnedAccess, returnedRefresh, err := connection.Tokens()
			Expect(err).To(HaveOccurred())
			Expect(returnedAccess).To(BeEmpty())
			Expect(returnedRefresh).To(BeEmpty())
		})
	})

	Describe("Client credentials grant", func() {
		It("Returns the access and refresh tokens generated by the server", func() {
			// Generate the tokens:
			accessToken := DefaultToken("Bearer", 5*time.Minute)
			refreshToken := DefaultToken("Refresh", 10*time.Hour)

			// Configure the server:
			oidServer.AppendHandlers(
				ghttp.CombineHandlers(
					VerifyClientCredentialsGrant("myclient", "mysecret"),
					RespondWithTokens(accessToken, refreshToken),
				),
			)

			// Create the connection:
			connection, err := NewConnectionBuilder().
				Logger(logger).
				Metrics(metrics).
				TokenURL(oidServer.URL()).
				URL(apiServer.URL()).
				Client("myclient", "mysecret").
				Build()
			Expect(err).ToNot(HaveOccurred())
			defer connection.Close()

			// Get the tokens:
			returnedAccess, returnedRefresh, err := connection.Tokens()
			Expect(err).ToNot(HaveOccurred())
			Expect(returnedAccess).To(Equal(accessToken))
			Expect(returnedRefresh).To(Equal(refreshToken))
		})

		It("Refreshes access token", func() {
			// Generate the tokens:
			expiredAccess := DefaultToken("Bearer", -5*time.Second)
			validAccess := DefaultToken("Bearer", 5*time.Minute)
			refreshToken := DefaultToken("Refresh", 10*time.Hour)

			// Configure the server:
			oidServer.AppendHandlers(
				ghttp.CombineHandlers(
					VerifyClientCredentialsGrant("myclient", "mysecret"),
					RespondWithTokens(expiredAccess, refreshToken),
				),
				ghttp.CombineHandlers(
					VerifyRefreshGrant(refreshToken),
					RespondWithTokens(validAccess, refreshToken),
				),
			)

			// Create the connection:
			connection, err := NewConnectionBuilder().
				Logger(logger).
				Metrics(metrics).
				TokenURL(oidServer.URL()).
				URL(apiServer.URL()).
				Client("myclient", "mysecret").
				Build()
			Expect(err).ToNot(HaveOccurred())
			defer connection.Close()

			// Get the tokens the first time:
			firstAccess, _, err := connection.Tokens()
			Expect(err).ToNot(HaveOccurred())
			Expect(firstAccess).To(Equal(expiredAccess))

			// Get the tokens the second time:
			secondAccess, _, err := connection.Tokens()
			Expect(err).ToNot(HaveOccurred())
			Expect(secondAccess).To(Equal(validAccess))
		})

		It("Requests a new refresh token when it expires", func() {
			// Generate the tokens:
			expiredAccess := DefaultToken("Bearer", -5*time.Second)
			expiredRefresh := DefaultToken("Refresh", -15*time.Second)
			validAccess := DefaultToken("Bearer", 5*time.Minute)
			validRefresh := DefaultToken("Refresh", 10*time.Hour)

			// Configure the server:
			oidServer.AppendHandlers(
				ghttp.CombineHandlers(
					VerifyClientCredentialsGrant("myclient", "mysecret"),
					RespondWithTokens(expiredAccess, expiredRefresh),
				),
				ghttp.CombineHandlers(
					VerifyClientCredentialsGrant("myclient", "mysecret"),
					RespondWithTokens(validAccess, validRefresh),
				),
			)

			// Create the connection:
			connection, err := NewConnectionBuilder().
				Logger(logger).
				Metrics(metrics).
				TokenURL(oidServer.URL()).
				URL(apiServer.URL()).
				Client("myclient", "mysecret").
				Build()
			Expect(err).ToNot(HaveOccurred())
			defer connection.Close()

			// Get the tokens the first time:
			_, firstRefresh, err := connection.Tokens()
			Expect(err).ToNot(HaveOccurred())
			Expect(firstRefresh).To(Equal(expiredRefresh))

			// Get the tokens the second time:
			_, secondRefresh, err := connection.Tokens()
			Expect(err).ToNot(HaveOccurred())
			Expect(secondRefresh).To(Equal(validRefresh))
		})

		It("Requests a new refresh token when it expires in less than ten seconds", func() {
			// Generate the tokens:
			expiredAccess := DefaultToken("Bearer", -5*time.Second)
			expiredRefresh := DefaultToken("Refresh", 5*time.Second)
			validAccess := DefaultToken("Bearer", 5*time.Minute)
			validRefresh := DefaultToken("Refresh", 10*time.Hour)

			// Configure the server:
			oidServer.AppendHandlers(
				ghttp.CombineHandlers(
					VerifyClientCredentialsGrant("myclient", "mysecret"),
					RespondWithTokens(expiredAccess, expiredRefresh),
				),
				ghttp.CombineHandlers(
					VerifyClientCredentialsGrant("myclient", "mysecret"),
					RespondWithTokens(validAccess, validRefresh),
				),
			)

			// Create the connection:
			connection, err := NewConnectionBuilder().
				Logger(logger).
				Metrics(metrics).
				TokenURL(oidServer.URL()).
				URL(apiServer.URL()).
				Client("myclient", "mysecret").
				Build()
			Expect(err).ToNot(HaveOccurred())
			defer connection.Close()

			// Get the tokens the first time:
			_, firstRefresh, err := connection.Tokens()
			Expect(err).ToNot(HaveOccurred())
			Expect(firstRefresh).To(Equal(expiredRefresh))

			// Get the tokens the second time:
			_, secondRefresh, err := connection.Tokens()
			Expect(err).ToNot(HaveOccurred())
			Expect(secondRefresh).To(Equal(validRefresh))
		})

		It("Fails with wrong client identifier", func() {
			// Configure the server:
			oidServer.AppendHandlers(
				ghttp.CombineHandlers(
					VerifyClientCredentialsGrant("badclient", "mysecret"),
					RespondWithError("invalid_grant", "Bad client"),
				),
			)

			// Create the connection:
			connection, err := NewConnectionBuilder().
				Logger(logger).
				Metrics(metrics).
				TokenURL(oidServer.URL()).
				URL(apiServer.URL()).
				Client("badclient", "mysecret").
				Build()
			Expect(err).ToNot(HaveOccurred())
			defer connection.Close()

			// Get the tokens:
			_, _, err = connection.Tokens()
			Expect(err).To(HaveOccurred())
		})

		It("Fails with wrong client secret", func() {
			// Configure the server:
			oidServer.AppendHandlers(
				ghttp.CombineHandlers(
					VerifyClientCredentialsGrant("myclient", "badsecret"),
					RespondWithError("invalid_grant", "Bad secret"),
				),
			)

			// Create the connection:
			connection, err := NewConnectionBuilder().
				Logger(logger).
				Metrics(metrics).
				TokenURL(oidServer.URL()).
				URL(apiServer.URL()).
				Client("myclient", "badsecret").
				Build()
			Expect(err).ToNot(HaveOccurred())
			defer connection.Close()

			// Get the tokens:
			_, _, err = connection.Tokens()
			Expect(err).To(HaveOccurred())
		})

		It("Requests new tokens if server returns 'invalid_grant' for refresh", func() {
			// Generate the tokens:
			oldAccess := DefaultToken("Bearer", -5*time.Second)
			oldRefresh := DefaultToken("Refresh", 10*time.Hour)
			newAccess := DefaultToken("Bearer", 5*time.Second)
			newRefresh := DefaultToken("Refresh", 10*time.Hour)

			// Configure the server:
			oidServer.AppendHandlers(
				ghttp.CombineHandlers(
					VerifyRefreshGrant(oldRefresh),
					RespondWithError("invalid_grant", "Session not active"),
				),
				ghttp.CombineHandlers(
					VerifyClientCredentialsGrant("myclient", "mysecret"),
					RespondWithTokens(newAccess, newRefresh),
				),
			)

			// Create the connection:
			connection, err := NewConnectionBuilder().
				Logger(logger).
				Metrics(metrics).
				TokenURL(oidServer.URL()).
				URL(apiServer.URL()).
				Client("myclient", "mysecret").
				Tokens(oldAccess, oldRefresh).
				Build()
			Expect(err).ToNot(HaveOccurred())
			defer connection.Close()

			// Get the tokens:
			returnedAccess, returnedRefresh, err := connection.Tokens()
			Expect(err).ToNot(HaveOccurred())
			Expect(returnedAccess).To(Equal(newAccess))
			Expect(returnedRefresh).To(Equal(newRefresh))
		})

		It("Honors cookies", func() {
			// Generate the tokens:
			expiredAccess := DefaultToken("Bearer", -5*time.Minute)
			validAccess := DefaultToken("Bearer", 5*time.Minute)
			refreshToken := DefaultToken("Refresh", 10*time.Hour)

			// Configure the server:
			oidServer.AppendHandlers(
				ghttp.CombineHandlers(
					RespondWithCookie("mycookie", "myvalue"),
					RespondWithTokens(expiredAccess, refreshToken),
				),
				ghttp.CombineHandlers(
					VerifyCookie("mycookie", "myvalue"),
					RespondWithTokens(validAccess, refreshToken),
				),
			)

			// Create the connection:
			connection, err := NewConnectionBuilder().
				Logger(logger).
				Metrics(metrics).
				TokenURL(oidServer.URL()).
				URL(apiServer.URL()).
				Client("myclient", "mysecret").
				Build()
			Expect(err).ToNot(HaveOccurred())
			defer connection.Close()

			// Request the tokens the first time. This will return an expired access
			// token and a valid refresh token.
			_, _, err = connection.Tokens()
			Expect(err).ToNot(HaveOccurred())

			// Request the tokens a second time, therefore forcing a refresh grant which
			// should honor the cookies returned in the first attempt:
			_, _, err = connection.Tokens()
			Expect(err).ToNot(HaveOccurred())
		})
	})

	Describe("Retry for getting access token", func() {
		It("Return access token after a few retries", func() {
			// Generate tokens:
			refreshToken := DefaultToken("Refresh", 10*time.Hour)
			accessToken := DefaultToken("Bearer", 5*time.Minute)

			oidServer.AppendHandlers(
				RespondWithContent(
					http.StatusInternalServerError,
					"text/plain",
					"Internal Server Error",
				),
				RespondWithContent(
					http.StatusBadGateway,
					"text/plain",
					"Bad Gateway",
				),
				ghttp.CombineHandlers(
					VerifyRefreshGrant(refreshToken),
					RespondWithTokens(accessToken, refreshToken),
				),
			)

			// Create the connection:
			connection, err := NewConnectionBuilder().
				Logger(logger).
				Metrics(metrics).
				TokenURL(oidServer.URL()).
				URL(apiServer.URL()).
				Tokens(refreshToken).
				Build()
			Expect(err).ToNot(HaveOccurred())
			defer connection.Close()

			// Get the tokens:
			returnedAccess, returnedRefresh, err := connection.Tokens()
			Expect(err).ToNot(HaveOccurred())
			Expect(returnedAccess).ToNot(BeEmpty())
			Expect(returnedRefresh).ToNot(BeEmpty())

			expectedLabels := prometheus.Labels{
				"attempt": "2",
				"code":    "502",
			}
			counter := connection.tokenCountMetric.With(expectedLabels)
			Expect(testutil.ToFloat64(counter)).To(Equal(1.0))
		})

		It("Test no retry when status is not http 5xx", func() {
			// Generate tokens:
			refreshToken := DefaultToken("Refresh", 10*time.Hour)
			accessToken := DefaultToken("Bearer", 5*time.Minute)

			oidServer.AppendHandlers(
				RespondWithContent(
					http.StatusInternalServerError,
					"text/plain",
					"Internal Server Error",
				),
				RespondWithJSON(
					http.StatusForbidden,
					"{}",
				),
				ghttp.CombineHandlers(
					VerifyRefreshGrant(refreshToken),
					RespondWithTokens(accessToken, refreshToken),
				),
			)

			// Create the connection:
			connection, err := NewConnectionBuilder().
				Logger(logger).
				Metrics(metrics).
				TokenURL(oidServer.URL()).
				URL(apiServer.URL()).
				Tokens(refreshToken).
				Build()
			Expect(err).ToNot(HaveOccurred())
			defer connection.Close()

			// Get the tokens:
			_, _, err = connection.Tokens()
			Expect(err).To(HaveOccurred())
		})

		It("Honours context timeout", func() {
			// Generate tokens:
			refreshToken := DefaultToken("Refresh", 10*time.Hour)
			accessToken := DefaultToken("Bearer", 5*time.Minute)

			// Configure the server with a handler that introduces an
			// artificial delay:
			oidServer.AppendHandlers(
				ghttp.CombineHandlers(
					http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
						time.Sleep(10 * time.Millisecond)
					}),
					VerifyRefreshGrant(refreshToken),
					RespondWithTokens(accessToken, refreshToken),
				),
			)

			// Create the connection:
			connection, err := NewConnectionBuilder().
				Logger(logger).
				Metrics(metrics).
				TokenURL(oidServer.URL()).
				URL(apiServer.URL()).
				Tokens(refreshToken).
				Build()
			Expect(err).ToNot(HaveOccurred())
			defer connection.Close()

			// Request the token with a timeout smaller than the artificial
			// delay introduced by the server:
			ctx, _ := context.WithTimeout(context.Background(), 5*time.Millisecond)
			_, _, err = connection.TokensContext(ctx)

			// The request should fail with a context deadline exceeded error:
			Expect(err).To(HaveOccurred())
			Expect(errors.Is(err, context.DeadlineExceeded)).To(BeTrue())
		})
	})
})

func VerifyPasswordGrant(user, password string) http.HandlerFunc {
	return ghttp.CombineHandlers(
		ghttp.VerifyRequest(http.MethodPost, "/"),
		ghttp.VerifyContentType("application/x-www-form-urlencoded"),
		ghttp.VerifyFormKV("grant_type", "password"),
		ghttp.VerifyFormKV("username", user),
		ghttp.VerifyFormKV("password", password),
	)
}

func VerifyClientCredentialsGrant(id, secret string) http.HandlerFunc {
	return ghttp.CombineHandlers(
		ghttp.VerifyRequest(http.MethodPost, "/"),
		ghttp.VerifyContentType("application/x-www-form-urlencoded"),
		ghttp.VerifyFormKV("grant_type", "client_credentials"),
		ghttp.VerifyFormKV("client_id", id),
		ghttp.VerifyFormKV("client_secret", secret),
	)
}

func VerifyRefreshGrant(refreshToken string) http.HandlerFunc {
	return ghttp.CombineHandlers(
		ghttp.VerifyRequest(http.MethodPost, "/"),
		ghttp.VerifyContentType("application/x-www-form-urlencoded"),
		ghttp.VerifyFormKV("grant_type", "refresh_token"),
		ghttp.VerifyFormKV("refresh_token", refreshToken),
	)
}

func RespondWithTokens(accessToken, refreshToken string) http.HandlerFunc {
	return RespondWithJSONTemplate(
		http.StatusOK,
		`{
			"access_token": "{{ .AccessToken }}",
			"refresh_token": "{{ .RefreshToken }}"
		}`,
		"AccessToken", accessToken,
		"RefreshToken", refreshToken,
	)
}

func RespondWithError(err, description string) http.HandlerFunc {
	return RespondWithJSONTemplate(
		http.StatusUnauthorized,
		`{
			"error": "{{ .Error }}",
			"error_description": "{{ .Description }}"
		}`,
		"Error", err,
		"Description", description,
	)
}
