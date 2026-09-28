FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/proofspan ./cmd/proofspan

FROM alpine:3.20
COPY --from=build /out/proofspan /usr/local/bin/proofspan
ENTRYPOINT ["proofspan"]
