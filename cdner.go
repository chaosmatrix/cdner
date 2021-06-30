package main

import (
	"bytes"
	"crypto/tls"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/miekg/dns"
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
	targetUrl                 = ""
	httpMaxConcurrency        = 10
	http2                     = false
	sni                       = ""
	cdnNodesStr               = ""
	cdnNodesFile              = ""
	httpMethod                = "GET"
	httpUserAgent             = "curl/7.61.0"
	httpContentType           = ""
	httpPayload               = ""
	httpConnectTimeout        = 30 * time.Second
	httpReadTimeout           = 30 * time.Second
	httpDiscardBody           = true
	httpOutputPath            = "/tmp/cdner"
	httpOutputFileSuffix      = "_response.body"
	dnsNameserversStr         = ""
	dnsNameserversFile        = ""
	dnsEcssStr                = ""
	dnsEcssFile               = ""
	dnsMaxConcurrency         = 5
	dnsTimeout                = 5 * time.Second
	dnsUseTcp                 = false
	dnsBufferSize        uint = 1232
)

func init() {
	flag.StringVar(&targetUrl, "target-url", targetUrl, "target url, example: https://localhost/index.png")
	flag.IntVar(&httpMaxConcurrency, "http-max-concurrency", httpMaxConcurrency, "max number of concurrency http request")
	flag.BoolVar(&http2, "http2", http2, "enable http2")
	flag.StringVar(&sni, "sni", sni, "TLS ServerNameIdentifier, default is the host of target-url if https enabled")
	flag.StringVar(&cdnNodesStr, "cdn-nodes", cdnNodesStr, "cdnnodes ip address seperate with ';'")
	flag.StringVar(&cdnNodesFile, "cdn-nodes-from-file", cdnNodesFile, "cdnnodes ip address from file, one line one")

	// fetcher
	flag.StringVar(&httpMethod, "http-method", httpMethod, "http method, compitable with rfc")
	flag.StringVar(&httpUserAgent, "http-user-agent", httpUserAgent, "set http user agent")
	flag.StringVar(&httpContentType, "http-content-type", httpContentType, "http content type")
	flag.StringVar(&httpPayload, "http-payload", httpPayload, "http request body direct send to remote server, without any formatted")
	flag.DurationVar(&httpConnectTimeout, "http-connect-timeout", httpConnectTimeout, "timeout in establishe connection")
	flag.DurationVar(&httpReadTimeout, "http-read-timeout", httpReadTimeout, "read timeout in established connection")
	flag.BoolVar(&httpDiscardBody, "http-discard-body", httpDiscardBody, "discard http response body")
	flag.StringVar(&httpOutputPath, "http-output-path", httpOutputPath, "when '--http-discard-body=false', output http response into file, store in this directory")
	flag.StringVar(&httpOutputFileSuffix, "http-output-file-suffix", httpOutputFileSuffix, "http response output filename's suffix")

	// resolver
	flag.StringVar(&dnsNameserversStr, "dns-nameservers", dnsNameserversStr, "nameservers ip address seperate with ';'")
	flag.StringVar(&dnsNameserversFile, "dns-nameservers-from-file", dnsNameserversFile, "nameservers ip address, one line one")
	flag.StringVar(&dnsEcssStr, "dns-ecss", dnsEcssStr, "enable edns-client-subnet feature, address seperate with ';', example: '1.2.3.4/30'")
	flag.StringVar(&dnsEcssFile, "dns-ecss-from-file", dnsEcssFile, "enable edns-client-subnet feature, address from file, one line one, example: '1.2.3.4/30'")
	flag.IntVar(&dnsMaxConcurrency, "dns-max-concurrency", dnsMaxConcurrency, "max number of concurrency dns request")
	flag.DurationVar(&dnsTimeout, "dns-timeout", dnsTimeout, "dns timeout")
	flag.BoolVar(&dnsUseTcp, "dns-use-tcp", dnsUseTcp, "dns query via tcp protocol")
	flag.UintVar(&dnsBufferSize, "dns-buffer-size", dnsBufferSize, "dns response buffer size")
}

func combineIpStrFile(_str, _file string) []string {
	var _array []string
	for _, _s := range strings.Split(_str, ";") {
		_s = strings.TrimSpace(strings.Trim(_s, "\n"))
		if _s == "" || strings.HasPrefix(_s, "#") {
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
		fmt.Fprintf(os.Stderr, "[+] Open file: '%s', Error: '%s'\n", _file, _err)
		return _array
	}
	for _, _byteline := range bytes.Split(_bs, []byte("\n")) {
		_byteline = bytes.TrimSpace(bytes.Trim(_byteline, "\n"))
		_line := string(_byteline)
		if _line == "" || strings.HasPrefix(_line, "#") {
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

func getEcss(_str, _file string) []string {
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
	_queryMsg := new(dns.Msg)
	_queryMsg.Id = dns.Id()
	_queryMsg.RecursionDesired = true
	_queryMsg.Question = make([]dns.Question, 1)
	if _, _, _err := net.SplitHostPort(_nameserver); _err != nil {
		_nameserver = _nameserver + ":" + "53"
	}
	_queryMsg.Question[0] = dns.Question{
		Name:   dns.Fqdn(_name),
		Qtype:  dns.TypeA,
		Qclass: dns.ClassINET,
	}

	// edns-client-subnet
	_IpNetmask := strings.Split(_ecs, "/")
	_ecs_ip, _ecs_netmask := _ecs, 30
	if len(_IpNetmask) == 2 {
		_ecs_ip = _IpNetmask[0]
		if _netmask, _err := strconv.Atoi(_IpNetmask[1]); _err == nil {
			_ecs_netmask = _netmask
		}
	}

	if _ip := net.ParseIP(_ecs_ip); _ip != nil {
		_opt := new(dns.OPT)
		_opt.Hdr.Name = "."
		_opt.Hdr.Rrtype = dns.TypeOPT
		// ipv6 mtu - udp header : 1280 - 48 = 1232
		if dnsBufferSize >= 512 && dnsBufferSize < 65536 {
			_opt.SetUDPSize(uint16(dnsBufferSize))
		} else {
			_opt.SetUDPSize(1232)
		}
		_opt_ecs := new(dns.EDNS0_SUBNET)
		if _ipv4 := _ip.To4(); _ipv4 != nil {
			_opt_ecs.Family = 1
			_opt_ecs.Address = _ipv4
		} else if _ipv6 := _ip.To16(); _ipv6 != nil {
			_opt_ecs.Family = 2
			_opt_ecs.Address = _ipv6
		}
		_opt_ecs.Code = dns.EDNS0SUBNET
		_opt_ecs.SourceNetmask = uint8(_ecs_netmask)
		_opt_ecs.SourceScope = uint8(_ecs_netmask)

		_opt.Option = append(_opt.Option, _opt_ecs)

		if _opt != nil {
			_queryMsg.Extra = []dns.RR{_opt}
		}
	}
	_client := new(dns.Client)
	if dnsTimeout < 1*time.Second {
		_client.Timeout = 5 * time.Second
	} else {
		_client.Timeout = dnsTimeout
	}
	if dnsUseTcp {
		_client.Net = "tcp"
	}
	_respMsg, _, _err := _client.Exchange(_queryMsg, _nameserver)
	if _err != nil {
		return _ips
	}
	for _, _rr := range _respMsg.Answer {
		if _a, _ok := _rr.(*dns.A); _ok {
			_ips = append(_ips, _a.A.String())
		}
	}
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

	var _httpReq *http.Request
	var _err error

	if httpPayload == "" {
		_httpReq, _err = http.NewRequest(httpMethod, req.url, nil)
	} else {
		_httpReq, _err = http.NewRequest(httpMethod, req.url, bytes.NewReader([]byte(httpPayload)))
	}
	if _err != nil {
		return nil, _err
	}
	// must set "Host" like this
	_httpReq.Host = req.host
	_httpReq.Header.Set("Host", req.host)
	_httpReq.Header.Set("User-Agent", httpUserAgent)
	if httpContentType != "" {
		_httpReq.Header.Set("Content-Type", httpContentType)
	}

	// send
	_resp, _err := _client.Do(_httpReq)
	if _err != nil {
		return nil, _err
	}
	if httpDiscardBody {
		// discard response body
		_resp.Body.Close()
	}
	return _resp, err
}

func main() {
	flag.Parse()
	if targetUrl == "" {
		flag.Usage()
		os.Exit(1)
	}

	// skip http method check
	httpMethod = strings.ToUpper(httpMethod)

	_urlStruct, _err := url.Parse(targetUrl)
	if _err != nil {
		panic(_err)
	}
	_host := _urlStruct.Hostname()
	// ip address maxlen = 15 '://' = 3
	_maxUrlLen := len(_urlStruct.Scheme+_urlStruct.Path+_urlStruct.RawQuery) + 15 + 3 // maxlen

	_cdnnodes := getCdnnodes(cdnNodesStr, cdnNodesFile)

	_nameservers := getNameServers(dnsNameserversStr, dnsNameserversFile)
	_nameservers = removeDuplicate(_nameservers)
	_ecss := getEcss(dnsEcssStr, dnsEcssFile)
	_ecss = removeDuplicate(_ecss)
	if len(_ecss) == 0 {
		_ecss = []string{""}
	}

	fmt.Printf("[+] %s\n", strings.Repeat("+ ", 6))
	for _, _ns := range _nameservers {
		for _, _ecs := range _ecss {
			_ips := lookupAWithEcs(_host, _ns, _ecs)
			sort.Strings(_ips)
			fmt.Printf("[+] Domain: '%s', NameServer: '%s', ECS: '%s', Answer: '%v'\n", _host, _ns, _ecs, _ips)
			_cdnnodes = append(_cdnnodes, _ips...)
		}
	}
	_cdnnodes = removeDuplicate(_cdnnodes)

	_oldHost := _host
	// do request
	var wg sync.WaitGroup
	_httpRateChan := make(chan struct{}, 1)
	if httpMaxConcurrency > 0 {
		if httpMaxConcurrency < len(_cdnnodes) {
			_httpRateChan = make(chan struct{}, httpMaxConcurrency)
		} else {
			_httpRateChan = make(chan struct{}, len(_cdnnodes))
		}
	}
	fmt.Printf("[+] %s\n", strings.Repeat("+ ", 6))
	for _, _node := range _cdnnodes {
		_hostPort := strings.Replace(_urlStruct.Host, _oldHost, _node, 1)
		_urlStruct.Host = _hostPort
		_url := _urlStruct.String()
		if httpMaxConcurrency > 0 {
			_httpRateChan <- struct{}{}
		}
		wg.Add(1)
		go func(_url, _node string) {
			//
			if sni == "" && strings.EqualFold(_urlStruct.Scheme, "https") {
				sni = _urlStruct.Hostname()
			}
			_req := request{
				url:            _url,
				http2:          false,
				host:           _host,
				sni:            sni,
				connectTimeout: httpConnectTimeout,
				readTimeout:    httpReadTimeout,
				//headers map[string][]string
			}
			_resp, _err := _req.send()
			if _err != nil {
				fmt.Printf("[+] URL: '%s', Host: '%s', Error: '%v'\n", _req.url, _host, _err)
			} else if _resp != nil {
				if !httpDiscardBody {
					if _, _err := os.Stat(httpOutputPath); os.IsNotExist(_err) {
						os.MkdirAll(httpOutputPath, 0750)
					}
					_fp := path.Join(httpOutputPath, _host+"_"+_node+httpOutputFileSuffix)
					if _fw, _err := os.OpenFile(_fp, os.O_CREATE|os.O_RDWR, 0500); _err == nil {
						io.Copy(_fw, _resp.Body)
						_fw.Close()
						_resp.Body.Close()
						fmt.Printf("[+] URL: '%s', Host: '%s', Sni: '%s', Status: '%d', Filename: '%s'\n", _req.url, _host, sni, _resp.StatusCode, _fp)
					} else {
						fmt.Printf("[+] URL: '%s', Host: '%s', Sni: '%s', Status: '%d', Error: '%v'\n", _req.url, _host, sni, _resp.StatusCode, _err)
					}
				} else {
					fmt.Printf("[+] URL: '%s', %sHost: '%s', Sni: '%s', Status: '%d'\n", _req.url, strings.Repeat(" ", _maxUrlLen-len(_req.url)), _host, sni, _resp.StatusCode)
				}
			} else {
				fmt.Printf("[+] URL: '%s', Host: '%s', Sni: '%s', Error: 'Both Http Response and Error is empty'\n", _req.url, _host, sni)
			}
			wg.Done()
			<-_httpRateChan
		}(_url, _node)
		_oldHost = _node
	}
	wg.Wait()
	fmt.Printf("[+] %s\n", strings.Repeat("+ ", 6))
}
