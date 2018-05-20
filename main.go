package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/user"
	"path"
	"strings"

	_ "github.com/mattn/go-sqlite3"
	"github.com/spf13/viper"
)

var (
	client        = http.DefaultClient
	cookie        *http.Cookie
	config        *Config
	defaultConfig = `{
		"username": "",
		"password": "",
		"domain": ""
	}`
)

// Config ...
type Config struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Domain   string `json:"domain"`
	IP       string `json:"ip"`
}

func main() {
	usr, err := user.Current()
	if err != nil {
		log.Fatal(err)
	}
	confpath := path.Join(usr.HomeDir, ".config")
	if _, err := os.Stat(confpath); os.IsNotExist(err) {
		os.Mkdir(confpath, 0700)
	}

	v := viper.New()
	v.SetConfigName("hover")
	v.SetConfigType("json")
	v.AddConfigPath(confpath)
	if _, ok := v.ReadInConfig().(viper.ConfigFileNotFoundError); ok {
		// Create config file
		newConf := []byte(defaultConfig)
		if err := ioutil.WriteFile(path.Join(confpath, "hover.json"), newConf, 0600); err != nil {
			log.Fatal(err)
		}
	}
	if err = v.Unmarshal(&config); err != nil {
		log.Fatal(err)
	}

	ip, err := getIP()
	if err != nil {
		log.Fatal(err)
	}
	if ip == config.IP {
		return
	}

	domains, err := login()
	if err != nil {
		log.Fatal(err)
	}

	if !domains[config.Domain] {
		log.Fatal("Domain not owned")
	}

	if err = updateDNS(ip); err != nil {
		log.Fatal(err)
	}

	config.IP = ip
	confBytes, err := json.MarshalIndent(config, "", "    ")
	if err != nil {
		log.Fatal(err)
	}
	if err := ioutil.WriteFile(path.Join(confpath, "hover.json"), confBytes, 0600); err != nil {
		log.Fatal(err)
	}
}

func getIP() (string, error) {
	out := &struct {
		IP string `json:"ip"`
	}{}

	resp, err := http.Get("https://myexternalip.com/json")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	content, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	if err = json.Unmarshal(content, out); err != nil {
		return "", err
	}

	return out.IP, nil
}

func login() (map[string]bool, error) {
	login := &struct {
		Succeeded bool     `json:"succeeded"`
		Domains   []string `json:"domains"`
	}{}

	v := url.Values{}
	v.Set("username", config.Username)
	v.Set("password", config.Password)

	req, err := http.NewRequest(http.MethodPost, "https://www.hover.com/api/login", strings.NewReader(v.Encode()))
	if err != nil {
		return nil, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	for _, c := range resp.Cookies() {
		if c.Name == "hoverauth" {
			cookie = c
		}
	}

	content, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if err = json.Unmarshal(content, login); err != nil {
		return nil, err
	}

	if !login.Succeeded {
		return nil, fmt.Errorf("Could not login")
	}

	domains := make(map[string]bool)
	for _, d := range login.Domains {
		domains[d] = true
	}

	return domains, nil
}

func updateDNS(ip string) error {
	type Entries struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		Type      string `json:"type"`
		Content   string `json:"content"`
		IsDefault bool   `json:"is_default"`
		CanRevert bool   `json:"can_revert"`
	}

	type Domain struct {
		DomainName string     `json:"domain_name"`
		ID         string     `json:"id"`
		Active     bool       `json:"active"`
		Entries    []*Entries `json:"entries"`
	}

	dns := &struct {
		Succeeded bool      `json:"succeeded"`
		Domains   []*Domain `json:"domains"`
	}{}

	req, err := http.NewRequest(http.MethodGet, "https://www.hover.com/api/dns", nil)
	if err != nil {
		return err
	}
	req.AddCookie(cookie)

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	content, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	if err = json.Unmarshal(content, dns); err != nil {
		return err
	}

	for _, d := range dns.Domains {
		if d.DomainName != config.Domain {
			continue
		}

		for _, e := range d.Entries {
			success := &struct {
				Succeeded bool   `json:"succeeded"`
				Error     string `json:"error"`
			}{}

			if e.Type != "A" {
				continue
			}

			req, err := http.NewRequest(http.MethodPut, fmt.Sprintf("https://www.hover.com/api/dns/%s", e.ID), nil)
			if err != nil {
				return err
			}
			req.AddCookie(cookie)

			q := req.URL.Query()
			q.Add("content", ip)
			req.URL.RawQuery = q.Encode()

			resp, err := client.Do(req)
			if err != nil {
				return err
			}
			defer resp.Body.Close()

			content, err := ioutil.ReadAll(resp.Body)
			if err != nil {
				return err
			}

			if err = json.Unmarshal(content, success); err != nil {
				return err
			}
			if !success.Succeeded {
				return fmt.Errorf(success.Error)
			}
		}
	}

	return nil
}
