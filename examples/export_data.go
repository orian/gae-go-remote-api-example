package main

// Simple tool which exports data to GAE through remote_api.
//
// This tool can be invoked using the goapp tool bundled with the SDK.
// $ goapp run export_data.go \
//   -email admin@example.com \
//   -host my-app@appspot.com \
//   -password_file ~/.my_password
//
// Or for a dev env on a localhost.
// $ goapp run export_data.go \
//   -email test@test.com     \
//   -host localhost:8080     \
//   -data_dir data/
//

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"appengine"
	"appengine/datastore"
	"appengine/remote_api"
)

var (
	host         = flag.String("host", "", "hostname of application")
	email        = flag.String("email", "", "email of an admin user for the application")
	passwordFile = flag.String("password_file", "", "file which contains the user's password")
	dataFile     = flag.String("data_file", "", "file which contains DataItem")
	dataDir      = flag.String("data_dir", "", "directory with files containing one item each")
	filePattern  = flag.String("file_pattern", `data_item_\d+.json`, "if --data_dir not empty, files matching this pattern will be parsed")
)

type DataItem struct {
	Name string
}

func LoadDataItem(filepath string) (*DataItem, error) {
	jsonBlob, err := ioutil.ReadFile(filepath)
	if err != nil {
		// fmt.Printf("so sad, cannot read file\n")
		return nil, err
	}
	dataItem := &DataItem{}
	err = json.Unmarshal(jsonBlob, dataItem)
	if err != nil {
		// fmt.Printf("so sad, cannot parse json %v\n", err)
		return nil, err
	}

	return dataItem, nil
}

const DataItemDatastoreKind = "DataItem"

func InsertToDatastore(context appengine.Context, dataItem DataItem) error {
	key := datastore.NewIncompleteKey(context, DataItemDatastoreKind, nil)
	_, err := datastore.Put(context, key, &dataItem)
	return err
}

func LoadAndInsert(context appengine.Context, path string) error {
	dataItem, err := LoadDataItem(path)
	if err != nil {
		return err
	}
	if err := InsertToDatastore(context, *dataItem); err != nil {
		return err
	}
	return nil
}

func CreateVisitInserter(reg_expression string, context appengine.Context) filepath.WalkFunc {
	re := regexp.MustCompile(reg_expression)
	return func(filePath string, f os.FileInfo, err error) error {
		if _, fileName := path.Split(filePath); re.MatchString(fileName) {
			fmt.Printf("Visited: %s\n", filePath)
			if err := LoadAndInsert(context, filePath); err != nil {
				log.Printf("didn't work out %s : %v", fileName, err)
			}
		} else {
			fmt.Printf("Skip: %s\n", fileName)
		}
		return nil
	}
}

func ExportDirectory(dataDir, filePattern string, context appengine.Context) error {
	err := filepath.Walk(dataDir, CreateVisitInserter(filePattern, context))
	fmt.Printf("filepath.Walk() returned %v\n", err)
	return err
}

func main() {
	flag.Parse()

	if *host == "" {
		log.Fatalf("Required flag: -host")
	}
	if *email == "" {
		log.Fatalf("Required flag: -email")
	}
	is_local := regexp.MustCompile(`.*(localhost|127\.0\.0\.1)`).MatchString(*host)
	if !is_local && *passwordFile == "" {
		log.Fatalf("Required flag: -password_file")
	}
	if *dataFile == "" && *dataDir == "" {
		log.Fatalf("Required flag: -data_file or -data_dir")
	}

	var client *http.Client
	if !is_local {
		p, err := ioutil.ReadFile(*passwordFile)
		if err != nil {
			log.Fatalf("Unable to read password from %q: %v", *passwordFile, err)
		}
		password := strings.TrimSpace(string(p))
		client = clientLoginClient(*host, *email, password)
	} else {
		client = clientLocalLoginClient(*host, *email)
	}

	c, err := remote_api.NewRemoteContext(*host, client)
	if err != nil {
		log.Fatalf("Failed to create context: %v", err)
	}
	log.Printf("App ID %q", appengine.AppID(c))

	if *dataFile != "" {
		dataItem, err := LoadDataItem(*dataFile)
		if err != nil {
			log.Fatalf("Failed to open file: %v", err)
		}
		if err := InsertToDatastore(c, *dataItem); err != nil {
			log.Fatalf("Failed to insert dataItem: %v", err)
		}
	} else {
		ExportDirectory(*dataDir, *filePattern, c)
	}
}

func clientLocalLoginClient(host, email string) *http.Client {
	jar, err := cookiejar.New(nil)
	if err != nil {
		log.Fatalf("failed to make cookie jar: %v", err)
	}
	client := &http.Client{
		Jar: jar,
	}
	local_login_url := fmt.Sprintf("http://%s/_ah/login?email=%s&admin=True&action=Login&continue=", host, email)
	resp, err := client.Get(local_login_url)
	if err != nil {
		log.Fatalf("could not post login: %v", err)
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		log.Fatalf("unsuccessful request: status %d; body %q", resp.StatusCode, body)
	}
	if err != nil {
		log.Fatalf("unable to read response: %v", err)
	}

	m := regexp.MustCompile(`Logged in`).FindSubmatch(body)
	if m == nil {
		log.Fatalf("no auth code in response %q", body)
	}

	return client
}

func clientLoginClient(host, email, password string) *http.Client {
	jar, err := cookiejar.New(nil)
	if err != nil {
		log.Fatalf("failed to make cookie jar: %v", err)
	}
	client := &http.Client{
		Jar: jar,
	}

	v := url.Values{}
	v.Set("Email", email)
	v.Set("Passwd", password)
	v.Set("service", "ah")
	v.Set("source", "Misc-remote_api-0.1")
	v.Set("accountType", "HOSTED_OR_GOOGLE")

	resp, err := client.PostForm("https://www.google.com/accounts/ClientLogin", v)
	if err != nil {
		log.Fatalf("could not post login: %v", err)
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		log.Fatalf("unsuccessful request: status %d; body %q", resp.StatusCode, body)
	}
	if err != nil {
		log.Fatalf("unable to read response: %v", err)
	}

	m := regexp.MustCompile(`Auth=(\S+)`).FindSubmatch(body)
	if m == nil {
		log.Fatalf("no auth code in response %q", body)
	}
	auth := string(m[1])

	u := &url.URL{
		Scheme:   "https",
		Host:     host,
		Path:     "/_ah/login",
		RawQuery: "continue=/&auth=" + url.QueryEscape(auth),
	}

	// Disallow redirects.
	redirectErr := errors.New("stopping redirect")
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		return redirectErr
	}

	resp, err = client.Get(u.String())
	if urlErr, ok := err.(*url.Error); !ok || urlErr.Err != redirectErr {
		log.Fatalf("could not get auth cookies: %v", err)
	}
	defer resp.Body.Close()

	body, err = ioutil.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusFound {
		log.Fatalf("unsuccessful request: status %d; body %q", resp.StatusCode, body)
	}

	client.CheckRedirect = nil
	return client
}
