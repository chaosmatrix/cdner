package main

import (
	"bytes"
	"crypto/tls"
	"flag"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

type request struct {
	url            string
	http2          bool
	host           string
	sni            string
	headers        map[string][]string
	connectTimeout time.Duration
	readTimeout    time.Duration
}

var (
	targetUrl       = ""
	http2           = false
	sni             = ""
	nameserversStr  = ""
	nameserversFile = ""
	cdnnodesStr     = ""
	cdnnodesFile    = ""
	connectTimeout  = 30 * time.Second
	readTimeout     = 30 * time.Second
)

func init() {
	flag.StringVar(&targetUrl, "target-url", targetUrl, "target url, example: https://localhost/index.png")
	flag.BoolVar(&http2, "http2", http2, "enable http2")
	flag.StringVar(&sni, "sni", sni, "TLS ServerNameIdentifier")
	flag.StringVar(&nameserversStr, "nameservers", nameserversStr, "nameservers ip address seperate with ';'")
	flag.StringVar(&nameserversFile, "ns-from-file", nameserversFile, "nameservers ip address, one line one")
	flag.StringVar(&cdnnodesStr, "cdnnodes", cdnnodesStr, "cdnnodes ip address seperate with ';'")
	flag.StringVar(&cdnnodesFile, "cdnnodes-from-file", cdnnodesFile, "cdnnodes ip address, one line one")
	flag.DurationVar(&connectTimeout, "connect-timeout", connectTimeout, "timeout in establishe connection")
	flag.DurationVar(&readTimeout, "read-timeout", readTimeout, "read timeout in established connection")
}

func combineIpStrFile(_str, _file string) []string {
	var _array []string
	for _, _s := range strings.Split(_str, ";") {
		_s = strings.TrimSpace(strings.Trim(_s, "\n"))
		if _s == "" {
			continue
		}
		// skip check
		//if net.ParseIP(_s) == nil {
		//	continue
		//}
		_array = append(_array, _s)
	}
	_bs, _err := ioutil.ReadFile(_file)
	if _err != nil {
		return _array
	}
	for _, _byteline := range bytes.Split(_bs, []byte("\n")) {
		_byteline = bytes.TrimSpace(bytes.Trim(_byteline, "\n"))
		_line := string(_byteline)
		if _line == "" {
			continue
		}
		// skip check
		//if net.ParseIP(_line) == nil {
		//	continue
		//}
		_array = append(_array, _line)
	}
	return _array
}

func getNameServers(_str, _file string) []string {
	return combineIpStrFile(_str, _file)
}

func getCdnnodes(_str, _file string) []string {
	return combineIpStrFile(_str, _file)
}

func removeDuplicate(_array []string) []string {
	if len(_array) == 0 {
		return _array
	}
	sort.Strings(_array)

	_slow, _fast := 0, 0
	for _fast < len(_array) {
		if _array[_slow] != _array[_fast] {
			_slow++
			_array[_slow], _array[_fast] = _array[_fast], _array[_slow]
		}
		_fast++
	}
	return _array[:_slow+1]
}

func lookupAWithEcs(_name, _nameserver, _ecs string) []string {
	var _ips []string
	// TODO
	return _ips
}

func (req request) send() (*http.Response, error) {
	var err error
	_client := new(http.Client)

	_tr := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   req.connectTimeout,
			DualStack: false,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		ResponseHeaderTimeout: req.readTimeout,
	}
	if !req.http2 {
		_tr.ForceAttemptHTTP2 = false
	}

	if req.sni != "" {
		_tlsConfig := new(tls.Config)
		_tlsConfig.ServerName = req.sni
		_tr.TLSClientConfig = _tlsConfig
	}

	_client.Transport = _tr

	_httpReq, _err := http.NewRequest("GET", req.url, nil)
	if _err != nil {
		return nil, _err
	}
	// must set "Host" like this
	_httpReq.Host = req.host
	_httpReq.Header.Add("Host", req.host)
	_httpReq.Header.Add("User-Agent", "curl/7.61.0")

	// send
	_resp, _err := _client.Do(_httpReq)
	if _err != nil {
		return nil, _err
	}
	return _resp, err
}

func main() {
	flag.Parse()
	if targetUrl == "" {
		flag.Usage()
	}

	_nameservers := getNameServers(nameserversStr, nameserversFile)
	_cdnnodes := getCdnnodes(cdnnodesStr, cdnnodesFile)
	_urlStruct, _err := url.Parse(targetUrl)
	if _err != nil {
		panic(_err)
	}
	_host := _urlStruct.Hostname()

	_nameservers = removeDuplicate(_nameservers)
	for _, _ns := range _nameservers {
		_ips := lookupAWithEcs(_host, _ns, "")
		for _, _ip := range _ips {
			_cdnnodes = append(_cdnnodes, _ip)
		}
	}
	_cdnnodes = removeDuplicate(_cdnnodes)

	var _urls []string
	_oldHost := _host
	for _, _node := range _cdnnodes {
		_hostPort := strings.Replace(_urlStruct.Host, _oldHost, _node, 1)
		_urlStruct.Host = _hostPort
		_urls = append(_urls, _urlStruct.String())
		_oldHost = _node
	}
	// do request
	var wg sync.WaitGroup
	for _, _url := range _urls {
		wg.Add(1)
		go func(_url string) {
			//
			_req := request{
				url:            _url,
				http2:          false,
				host:           _host,
				sni:            sni,
				connectTimeout: connectTimeout,
				readTimeout:    readTimeout,
				//headers map[string][]string
			}
			_resp, _err := _req.send()
			if _err != nil {
				fmt.Printf("URL: '%s', Host: '%s', Error: '%v'\n", _req.url, _host, _err)
			} else if _resp != nil {
				fmt.Printf("URL: '%s', Host: '%s', Status: '%d'\n", _req.url, _host, _resp.StatusCode)
			}
			wg.Done()
		}(_url)
	}
	wg.Wait()
}
