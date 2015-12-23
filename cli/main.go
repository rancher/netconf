package main

import (
	"io"
	"io/ioutil"
	"log"
	"os"

	"gopkg.in/yaml.v2"

	"github.com/Sirupsen/logrus"
	"github.com/rancher/netconf"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	logrus.SetLevel(logrus.DebugLevel)
	var input io.Reader = os.Stdin
	if len(os.Args) > 1 {
		var err error
		input, err = os.Open(os.Args[1])
		if err != nil {
			return err
		}
	}

	content, err := ioutil.ReadAll(input)
	if err != nil {
		return err
	}

	var netCfg netconf.NetworkConfig
	err = yaml.Unmarshal(content, &netCfg)
	if err != nil {
		return err
	}

	return netconf.ApplyNetworkConfigs(&netCfg)
}
