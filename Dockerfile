FROM golang AS builder

WORKDIR /src
COPY . .
ENV CGO_ENABLED=0
RUN go build -v

FROM alpine

EXPOSE 2112/tcp

COPY --from=builder /src/freezebot /bin/freezebot
COPY --from=builder /src/config.json /etc/freezebot/config.json

ENTRYPOINT ["/bin/freezebot", "-config", "/etc/freezebot/config.json"]
