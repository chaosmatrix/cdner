# cdner

### Usage
```
root@localhost:~/cdner$ ./cdner --target-url https://github.githubassets.com/favicons/favicon.png --sni github.githubassets.com --dns-nameservers "8.8.8.8;1.1.1.1"
[+] + + + + + +
[+] Domain: 'github.githubassets.com', NameServer: '1.1.1.1', ECS: '', Answer: '[185.199.108.154 185.199.109.154 185.199.110.154 185.199.111.154]'
[+] Domain: 'github.githubassets.com', NameServer: '8.8.8.8', ECS: '', Answer: '[185.199.108.154 185.199.109.154 185.199.110.154 185.199.111.154]'
[+] + + + + + +
[+] URL: 'https://185.199.109.154/favicons/favicon.png', Host: 'github.githubassets.com', Status: '200'
[+] URL: 'https://185.199.108.154/favicons/favicon.png', Host: 'github.githubassets.com', Status: '200'
[+] URL: 'https://185.199.110.154/favicons/favicon.png', Host: 'github.githubassets.com', Status: '200'
[+] URL: 'https://185.199.111.154/favicons/favicon.png', Host: 'github.githubassets.com', Status: '200'
```
