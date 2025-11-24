package app_info

import "os"

var Hostname string

func init() {
	var err error
	Hostname, err = os.Hostname()
	if err != nil {
		panic(err)
	}
}
