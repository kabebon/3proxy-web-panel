FROM alpine:edge

RUN apk add --no-cache 3proxy --repository=http://dl-cdn.alpinelinux.org/alpine/edge/testing/

# Create required directories
RUN mkdir -p /etc/3proxy /var/log/3proxy

# Write a minimal bootstrap config (will be overwritten by panel)
RUN echo 'nserver 8.8.8.8' > /etc/3proxy/3proxy.cfg && \
    echo 'nscache 65536' >> /etc/3proxy/3proxy.cfg && \
    echo 'log /var/log/3proxy/3proxy.log D' >> /etc/3proxy/3proxy.cfg && \
    echo 'rotate 30' >> /etc/3proxy/3proxy.cfg && \
    echo 'auth strong' >> /etc/3proxy/3proxy.cfg && \
    echo 'users nobody:CR1:d41d8cd98f00b204e9800998ecf8427e' >> /etc/3proxy/3proxy.cfg && \
    echo 'deny *' >> /etc/3proxy/3proxy.cfg && \
    echo 'proxy -p3128' >> /etc/3proxy/3proxy.cfg

# Entrypoint: run 3proxy, restart on SIGHUP by using exec
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

EXPOSE 3128 1080

ENTRYPOINT ["/entrypoint.sh"]
