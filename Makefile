MAKEFLAGS += --no-print-directory

.PHONY: pull
pull:
	git pull

.PHONY: kill
kill:
	sudo docker kill ippon

.PHONY: start
start:
	sudo docker-compose up -d

.PHONY: build
build:
	@$(MAKE) pull
	go mod download
	GOARCH=arm GOARM=7 GOOS=linux go build -o snmp_ex .
	chmod +x snmp_ex
	sudo docker kill ippon
	sudo docker rm ippon
	sudo docker build -t ippon --no-cache .
	sudo docker-compose up -d
	sudo docker logs ippon