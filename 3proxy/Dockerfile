# 3proxy built from the upstream release source instead of the alpine
# testing package: the packaged 0.9.6 build rejects `countin`/`countall`
# ("Unknown command"), and those ACLs are what gives per-user byte-level
# traffic enforcement (hard cut mid-connection, like the VPN panels do).
# Pin the release explicitly — behavior of a proxy core must be reproducible.

FROM alpine:3.19 AS builder
RUN apk add --no-cache build-base git linux-headers
ARG TPROXY_VERSION=1.0.0
RUN git clone --depth 1 --branch ${TPROXY_VERSION} https://github.com/3proxy/3proxy /tmp/3proxy \
    && cd /tmp/3proxy \
    && make -f Makefile.Linux \
    && cp src/bin/3proxy /usr/local/bin/3proxy

FROM alpine:3.19
COPY --from=builder /usr/local/bin/3proxy /usr/bin/3proxy

# Create required directories
RUN mkdir -p /etc/3proxy

# Write a minimal bootstrap config (will be overwritten by panel)
RUN echo 'nserver 8.8.8.8' > /etc/3proxy/3proxy.cfg && \
    echo 'nscache 65536' >> /etc/3proxy/3proxy.cfg && \
    echo 'log /var/log/3proxy/3proxy.log D' >> /etc/3proxy/3proxy.cfg && \
    echo 'rotate 30' >> /etc/3proxy/3proxy.cfg && \
    echo 'auth strong' >> /etc/3proxy/3proxy.cfg && \
    echo 'users nobody:CL:nobody' >> /etc/3proxy/3proxy.cfg && \
    echo 'deny *' >> /etc/3proxy/3proxy.cfg && \
    echo 'proxy -p3128' >> /etc/3proxy/3proxy.cfg

# Entrypoint: 3proxy does not live-reload; SIGHUP means "restart me"
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

EXPOSE 3128 1080

ENTRYPOINT ["/entrypoint.sh"]
