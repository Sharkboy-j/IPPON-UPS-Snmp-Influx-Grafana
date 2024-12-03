FROM alpine:3.20
WORKDIR /
COPY snmp_ex /
COPY config.yml /
RUN mkdir mibs
COPY ./mibs/EPPC-MIB.yaml /mibs

CMD ["/snmp_ex"]