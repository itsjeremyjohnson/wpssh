# WordPress files, wp-cli and sshd for run.sh. The database is a separate
# mariadb container on the same network.
FROM wordpress:cli
USER root
RUN apk add --no-cache openssh-server bash python3 \
 && adduser -D -s /bin/bash -u 1000 wpuser \
 && sed -i 's/^wpuser:!/wpuser:*/' /etc/shadow \
 && mkdir -p /etc/ssh/authorized_keys \
 && chown -R wpuser /var/www/html
USER wpuser
RUN php -d memory_limit=512M /usr/local/bin/wp core download --path=/var/www/html
USER root
COPY sshd_config /etc/ssh/sshd_config
COPY wpe-gateway /usr/local/bin/wpe-gateway
COPY mu-plugins/ /var/www/html/wp-content/mu-plugins/
RUN chmod 755 /usr/local/bin/wpe-gateway && chown -R wpuser /var/www/html/wp-content/mu-plugins
CMD ["sh", "-c", "ssh-keygen -A >/dev/null && exec /usr/sbin/sshd -D -e"]
