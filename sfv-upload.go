package main

import (
	"archive/zip"
	"bytes"
	"crypto/tls"
	"encoding/xml"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strings"
)

const (
	SLASH = "/"
)

var (
	ArtifactId = flag.String("artifactId", "", "GAV: Artifact ID")
	GroupId    = flag.String("groupId", "", "GAV: Group ID")
	Version    = flag.String("version", "0.0.0", "GAV: Version")
	Packaging  = flag.String("packaging", "zip", "GAV: Packaging")

	Url      = flag.String("url", "localhost:/nexus/content/repositories/APDS", "Vendor delivery store URL")
	Username = flag.String("username", "", "Basic auth username")
	Password = flag.String("password", "", "Basic auth password")

	Dryrun             = flag.Bool("norun", false, "dryrun, do not upload anything")
	InsecureSkipVerify = flag.Bool("insecureSkipVerify", true, "Skip SSL certificate verification")
	Verbose            = flag.Bool("debug", false, "Show verbose debug information")

	In  = flag.String("in", "", "Input (filename or empty for stdin")
	Out = flag.String("out", "", "Output (filename or empty for stdin")
)

type Gav struct {
	GroupId, ArtifactId, Version, Packaging string
	// Optional
	Classifier string
}

type Delivery struct {
	// make sure xml element is lowercase
	XMLName xml.Name `xml:"delivery"`
	Report  Report   `xml:"report"`
}

type Report struct {
	ReturnValue     string         `xml:"returnValue"`
	HighestSeverity string         `xml:"highestSeverity"`
	Notifications   []Notification `xml:"notification"`
}

type Notification struct {
	Id       int    `xml:"id,attr"`
	Severity string `xml:"severity"`
	Message  string `xml:"message"`
}

// Return a maven default layout for a given Gav
// The maven spec looks like /com/db/cds/a1/0.0.0.0/a1-0.0.0.0.jar
func (g Gav) DefaultLayout() string {
	group := strings.Replace(g.GroupId, ".", SLASH, -1) // <0 means ALL
	completeArtifact := g.ArtifactId + "-" + g.Version
	// Optionally add classifier
	if len(g.Classifier) > 0 {
		completeArtifact = completeArtifact + "-" + g.Classifier
	}
	return strings.Join([]string{
		"", // force leading slash
		group,
		g.ArtifactId,
		g.Version,
		completeArtifact + "." + g.Packaging}, SLASH)
}

// Upload an artifact to a maven repository manager in default layout
func main() {
	flag.Parse()

	reader, writer := sfv.InOutFiles(*In, *Out)

	// Disable logging
	if !*Verbose {
		log.SetOutput(ioutil.Discard)
	}

	gav := Gav{GroupId: *GroupId, ArtifactId: *ArtifactId, Version: *Version, Packaging: *Packaging}
	url := *Url + gav.DefaultLayout()

	// Setup custom transport to allow ignoring SSL certificate checks (self signed, expired, ...)
	client := http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: *InsecureSkipVerify}}}

	req, err := http.NewRequest("PUT", url, reader)
	sfv.Die(err)

	// Add basic authorization
	if "" != *Username && "" != *Password {
		req.SetBasicAuth(*Username, *Password)
	}

	if *Dryrun {
		log.Println("dryrun - skipping upload")
	} else {
		resp, err := client.Do(req)
		sfv.Die(err)
		log.Printf("Received response %v\n", *resp)
		if resp.StatusCode != http.StatusCreated {
			fmt.Println("Upload failed: %s\n", resp)
			fmt.Println("Remember to enable/ disable environment variables http_proxy and/or no_proxy")
			os.Exit(2)
		}
	}

	// Show qa report for this delivery
	gav.Classifier = "qareport"
	reportUrl := *Url + gav.DefaultLayout()
	log.Printf("Fetching QA report from %s\n", reportUrl)
	req2, err := http.NewRequest("GET", *Url+gav.DefaultLayout(), nil)
	// Add basic authorization
	if "" != *Username && "" != *Password {
		req2.SetBasicAuth(*Username, *Password)
	}
	r2, err := client.Do(req2)
	sfv.Die(err)
	if r2.StatusCode != http.StatusOK {
		log.Fatalf("Cannot find QA report using %s: http return code %s\n", reportUrl, r2.StatusCode)
	}
	defer r2.Body.Close()

	delivery := Delivery{}

	// reports are small, just slurp in
	zipBuffer, err := ioutil.ReadAll(r2.Body)
	sfv.Die(err)
	zipReader, err := zip.NewReader(bytes.NewReader(zipBuffer), int64(len(zipBuffer)))
	sfv.Die(err)
	for _, f := range zipReader.File {
		fmt.Printf("Contents of %s:\n", f.Name)
		rc, err := f.Open()
		sfv.Die(err)
		if err == nil {
			defer rc.Close()
			reportBuffer, err := ioutil.ReadAll(rc)
			log.Printf("About to unmarshal %v\n", string(reportBuffer))
			err = xml.Unmarshal(reportBuffer, &delivery)
			sfv.Die(err)
		}
	}

	// Show report results in standard error format
	// <file>:<line no>: error: message
	log.Printf("Raw report: %s\n", delivery)
	for _, n := range delivery.Report.Notifications {
		fmt.Fprintf(writer, "%02d %-8s %s\n", n.Id, n.Severity, n.Message)
	}

	// Determine return code
	for _, n := range delivery.Report.Notifications {
		// Fail fast on first ERROR/ FATAL
		if "ERROR" == n.Severity || "FATAL" == n.Severity {
			os.Exit(3)
		}
	}
}
