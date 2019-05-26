SHELL=/bin/bash
include ./config.mk
build-app:
	go install ./stream.go

run:
	go run ./stream.go

release:
	CGO_ENABLED=0 GOOS=linux go install -a ./stream.go
	mkdir -p ./build
	rm -rf ./build/*
	cp $(GOPATH)/bin/stream ./build
	cp Dockerfile ./build
	docker build ./build --tag stream --tag gcr.io/$(PROJECT)/stream
	docker push gcr.io/$(PROJECT)/stream

push:
	gcloud beta run deploy stream \
	   --allow-unauthenticated --region $(REGION) \
		 --image gcr.io/$(PROJECT)/stream \
		 --set-env-vars "$(shell cat config.mk | sed 's#export ##' | grep -v "^PORT=" | tr '\n' ',')"

start_datastore_emulator:
	 echo To attach run:
	 echo "  export DATASTORE_EMULATOR_HOST=0.0.0.0:8000"
	 docker run -ti -p 8000:8000 google/cloud-sdk:latest gcloud beta emulators datastore start \
		 --no-store-on-disk --project test-project --host-port 0.0.0.0:8000 \
		 --consistency=1.0

test:
	go test ./...
