SHELL=/bin/bash
PROJECT=`jq -r .PROJECT config.json`
REGION=`jq -r .REGION config.json`

build-app:
	go install ./stream.go

run:
	go run ./stream.go --local

release:
	-rm -rf ./build/*
	mkdir -p ./build
	GOBIN=`pwd`/build CGO_ENABLED=0 GOOS=linux go install -a .
	install -d  ./build/usr/local/stream-run/templates
	install ./templates/* ./build/usr/local/stream-run/templates
	install -d  ./build/usr/local/stream-run/images
	install ./images/* ./build/usr/local/stream-run/images
	install ./config.json ./build/usr/local/stream-run/config.json
	cp Dockerfile ./build
	docker build ./build --tag stream --tag gcr.io/$(PROJECT)/stream
	docker push gcr.io/$(PROJECT)/stream

push: release
	gcloud beta run deploy stream \
	   --allow-unauthenticated --region $(REGION) \
		 --image gcr.io/$(PROJECT)/stream

start_datastore_emulator:
	 echo To attach run:
	 echo "  export DATASTORE_EMULATOR_HOST=0.0.0.0:8000"
	 docker run -ti -p 8000:8000 google/cloud-sdk:latest gcloud beta emulators datastore start \
		 --no-store-on-disk --project test-project --host-port 0.0.0.0:8000 \
		 --consistency=1.0

test:
	go test ./...
