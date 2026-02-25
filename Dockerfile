FROM golang:1.25 AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o /out/echoflow-api ./cmd/echoflow-api

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /
COPY --from=build /out/echoflow-api /echoflow-api
EXPOSE 8080
ENTRYPOINT ["/echoflow-api"]
