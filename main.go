package main

import (
  "os"
  "os/signal"
  "context"
  "fmt"
  "github.com/labstack/echo/v4"
  "github.com/labstack/echo/v4/middleware"
  "log"
  "time"
  "net/http"
  "strings"
  "encoding/json"
  "github.com/getsentry/sentry-go"
  sentryecho "github.com/getsentry/sentry-go/echo"
  "github.com/joho/godotenv"
  "github.com/kelseyhightower/envconfig"
)

type Config struct {
  SentryDSN             string `envconfig:"SENTRY_DSN"`
  LogFilePath           string `envconfig:"LOG_FILE_PATH"`
  Port                  int    `envconfig:"PORT" default:"3000"`
}

type LNResponse struct {
    Lnurlp interface{} `json:"lnurlp"`
    Keysend interface{} `json:"keysend"`
}


func GetJSON(url string) (interface{}, *http.Response, error) {
  response, err := http.Get(url)
  if err != nil || response.StatusCode > 300  {
    return nil, response, fmt.Errorf("No details: %s - %v", url, err)
  } else {
    defer response.Body.Close()
    var j interface{}
    err = json.NewDecoder(response.Body).Decode(&j)
    if err != nil {
      return nil, response, fmt.Errorf("Invalid JSON: %v", err)
    } else {
      return j, response, nil
    }
  }
}

func ToUrl(identifier string) (string, string, error) {
  parts := strings.Split(identifier, "@")
  if len(parts) != 2 {
    return "", "", fmt.Errorf("Invalid lightning address %s", identifier)
  }

  keysendUrl := fmt.Sprintf("https://%s/.well-known/keysend/%s", parts[1], parts[0])
  lnurlpUrl := fmt.Sprintf("https://%s/.well-known/lnurlp/%s", parts[1], parts[0])

  return lnurlpUrl, keysendUrl, nil
}

func main() {
  c := &Config{}

  // Load configruation from environment variables
  err := godotenv.Load(".env")
  if err != nil {
    fmt.Println("Failed to load .env file")
  }
  err = envconfig.Process("", c)
  if err != nil {
    log.Fatalf("Error loading environment variables: %v", err)
  }

  e := echo.New()
  e.HideBanner = true
  e.Use(middleware.Logger())
  e.Use(middleware.Recover())
  e.Use(middleware.RequestID())
  e.Use(middleware.CORS())

  // Setup exception tracking with Sentry if configured
  if c.SentryDSN != "" {
    if err = sentry.Init(sentry.ClientOptions{
      Dsn:          c.SentryDSN,
      IgnoreErrors: []string{"401"},
    }); err != nil {
      log.Printf("sentry init error: %v", err)
    }
    defer sentry.Flush(2 * time.Second)
    e.Use(sentryecho.New(sentryecho.Options{}))
  }

  e.GET("/lightning-address-details", func(c echo.Context) error {
    responseBody := &LNResponse{}

    ln := c.QueryParam("ln")
    lnurlpUrl, keysendUrl, err := ToUrl(ln)
    if err != nil {
      return c.JSON(http.StatusBadRequest, &responseBody)
    }

    lnurlp, lnurlpResponse, err := GetJSON(lnurlpUrl)
    if err != nil {
      e.Logger.Errorf("%v", err)
    } else {
      responseBody.Lnurlp = lnurlp
    }

    keysend, keysendResponse, err := GetJSON(keysendUrl)
    if err != nil {
      e.Logger.Errorf("%v", err)
    } else {
      responseBody.Keysend = keysend
    }

    // if both requests resulted in errors return a bad request. something must be wrong with the ln address
    if lnurlpResponse == nil && keysendResponse == nil {
      return c.JSON(http.StatusBadRequest, &responseBody)
    }
    // if both response have no success
    if lnurlpResponse != nil && keysendResponse != nil && lnurlpResponse.StatusCode > 300 && keysendResponse.StatusCode > 300 {
      return c.JSONPretty(lnurlpResponse.StatusCode, &responseBody, "  ")
    }

    // default return response
    return c.JSONPretty(http.StatusOK, &responseBody, "  ")
  })

  // Start server
  go func() {
    if err := e.Start(fmt.Sprintf(":%v", c.Port)); err != nil && err != http.ErrServerClosed {
      e.Logger.Fatal("shutting down the server")
    }
  }()
  // Wait for interrupt signal to gracefully shutdown the server with a timeout of 10 seconds.
  // Use a buffered channel to avoid missing signals as recommended for signal.Notify
  quit := make(chan os.Signal, 1)
  signal.Notify(quit, os.Interrupt)
  <-quit
  ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
  defer cancel()
  if err := e.Shutdown(ctx); err != nil {
    e.Logger.Fatal(err)
  }

}
