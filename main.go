package main

import (
	"bufio"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const (
	// SLASH is the forward slas
	SLASH = "/"
)

var (
	servername = flag.String("servername", "localhost", "Nexus server IP")
	port       = flag.String("port", "8081", "Nexus server port")
	username   = flag.String("username", "admin", "Nexus user")
	password   = flag.String("password", "admin123", "Nexus password")
	repository = flag.String("repository", "releases",
		"repository to sync against")
	upload = flag.Bool("upload", false,
		"Upload non-existing artifacts")
)

// Gav holds Maven group, artifact, version (plus packaging aka extension and
// classifier)
type Gav struct {
	GroupID, ArtifactID, Version, Packaging string
	// Optional
	Classifier string
}

// Artifact holds on item in the local repository to sync against Nexus
type Artifact struct {
	Path            string
	Filename        string
	Gav             Gav
	NexusStatusCode int
}

func main() {
	// Commandline parameters
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s <local folder>\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "       local folder must be a "+
			"hierarchy in Maven default layout, e.g. "+
			"${HOME}/.m2/repository\n")
		flag.PrintDefaults()
		os.Exit(1)
	}
	flag.Parse()
	if flag.NArg() == 0 {
		flag.Usage()
	}

	// Process arguments
	var as []Artifact
	for _, folder := range flag.Args() {
		filepath.Walk(folder, func(filename string, fi os.FileInfo,
			err error) error {
			if err != nil {
				// Cannot visit
				log.Printf("Skipping %s: %s\n",
					filename, err)
				// continue
				return nil
			}

			// filename is the complete thing below folder
			// log.Printf("checking %s\n", filename)
			// Ignore hidden items such as Nexus cache, trash, ...
			if filepath.HasPrefix(fi.Name(), ".") {
				log.Printf("Skipping %s\n", filename)
				return filepath.SkipDir
			}

			if fi.Mode().IsRegular() && consider(fi.Name()) {
				path := folder
				a, err := filepath.Rel(path, filename)
				if err != nil {
					log.Fatal(err)
				}
				as = append(as, Artifact{
					Path:     path,
					Filename: a,
					Gav:      DefaultLayout(a)})
			}
			return nil
		})
	}
	// Check if artifacts exist in repository
	client := &http.Client{}
	for _, a := range as {
		// HEAD
		url := fmt.Sprintf(
			"http://%s:%s/nexus/content/repositories/%s/%s",
			*servername, *port, *repository,
			filepath.ToSlash(a.Filename))
		req, err := http.NewRequest(http.MethodHead, url, nil)
		if err != nil {
			log.Fatal(err)
		}
		if len(*username) > 0 && len(*password) > 0 {
			req.SetBasicAuth(*username, *password)
		}
		res, err := client.Do(req)
		if err != nil {
			log.Fatal(err)
		}
		defer res.Body.Close()

		if res.StatusCode != http.StatusNotFound &&
			res.StatusCode != http.StatusOK {
			log.Fatalf("Expected %d or %d but got %d (%s)\n",
				http.StatusNotFound,
				http.StatusOK,
				res.StatusCode,
				url)
		}
		a.NexusStatusCode = res.StatusCode

		// PUT
		if a.NexusStatusCode == http.StatusNotFound && *upload {
			file, err := os.Open(filepath.Join(a.Path, a.Filename))
			if err != nil {
				log.Fatal(err)
			}
			defer file.Close()

			r := bufio.NewReader(file)
			req, err := http.NewRequest(http.MethodPut, url, r)
			if len(*username) > 0 && len(*password) > 0 {
				req.SetBasicAuth(*username, *password)
			}
			res, err := client.Do(req)
			if err != nil {
				log.Fatal(err)
			}
			defer res.Body.Close()

			if res.StatusCode != http.StatusCreated {
				log.Fatalf("Expected %d but got %d (%s)\n",
					http.StatusCreated,
					res.StatusCode,
					url)
			}
			a.NexusStatusCode = res.StatusCode
		}
		fmt.Printf("%+v\n", a)
	}
}

func consider(filename string) bool {
	return strings.HasSuffix(filename, ".jar") ||
		strings.HasSuffix(filename, ".pom")
}

// DefaultLayout determines Maven GAV from a given filename
// <group>/<group>/<group>/<artifact>/<version>/
//      <artifact>-<version>[-classifier].<packaging>
func DefaultLayout(path string) Gav {
	filename := filepath.Base(path)
	packaging := filepath.Ext(filename)
	p1 := filepath.Dir(path)
	version := filepath.Base(p1)
	p2 := filepath.Dir(p1)
	artifact := filepath.Base(p2)
	p3 := filepath.Dir(p2)
	group := filepath.FromSlash(filepath.Base(p3))

	// <artifact>-<version>-classifier.<packaging>
	var classifier string
	fromIndex := len(artifact) + 1 + len(version)
	toIndex := len(filename) - len(packaging)
	if fromIndex < toIndex {
		classifier = filename[fromIndex+1 : toIndex]
	}

	gav := Gav{
		GroupID:    group,
		ArtifactID: artifact,
		Version:    version,
		Classifier: classifier,
		Packaging:  packaging[1:], // strip '.'
	}
	log.Printf("%s -> %+v\n", path, gav)
	return gav
}

// DefaultLayout returns a maven default layout for a given Gav
// The maven spec looks like /com/company/prod/a1/0.0.0.0/a1-0.0.0.0.jar
func (g Gav) DefaultLayout() string {
	group := strings.Replace(g.GroupID, ".", SLASH, -1) // <0 means ALL
	completeArtifact := g.ArtifactID + "-" + g.Version
	// Optionally add classifier
	if len(g.Classifier) > 0 {
		completeArtifact = completeArtifact + "-" + g.Classifier
	}
	return strings.Join([]string{
		"", // force leading slash
		group,
		g.ArtifactID,
		g.Version,
		completeArtifact + "." + g.Packaging}, SLASH)
}
