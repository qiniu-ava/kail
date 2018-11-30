FROM busybox:glibc

ADD ./kail-linux /usr/bin/kail

ENTRYPOINT ["kail"]
