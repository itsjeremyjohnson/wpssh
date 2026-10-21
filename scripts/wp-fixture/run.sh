#!/usr/bin/env bash
# Runs wpgo value writes against a disposable WordPress on this Docker host
# and compares each with native wp-cli run in the container with exact argv.
#
#   scripts/wp-fixture/run.sh SCRATCH LABEL=WPGO_BINARY...
#
# SCRATCH holds the client key, wpgo's HOME and logs/<LABEL>.log. Each
# WPGO_BINARY is a linux wpgo build, e.g. from
#   CGO_ENABLED=0 GOOS=linux go build -o wpgo-head ./cmd/wpgo
# The fixture is a mariadb container and a WordPress+wp-cli+sshd container on
# a private network named $WPGO_FIXTURE_PREFIX-* (default wpgo249-*). sshd is
# published on 127.0.0.1 only, ports $WPGO_FIXTURE_PORT (default 22490) to
# +5. wpgo runs with HOME in SCRATCH and no SSH agent, so it reaches only the
# fixture. Everything is removed on exit; logs/teardown.log records it.
#
# Each case runs once per route: native wp first, then wpgo on a standard
# host (std), on the WP Engine gateway model (wpe, see wpe-gateway), and the
# FORCE_USER_LOGIN and damaged-argument variants. A route's verdict is MATCH
# when its exit status (zero or not) and read-back equal the first route's.
set -uo pipefail

here=$(cd "$(dirname "$0")" && pwd)
scratch=${1:?usage: run.sh SCRATCH LABEL=WPGO_BINARY...}
shift
mkdir -p "$scratch/logs"
scratch=$(cd "$scratch" && pwd)
prefix=${WPGO_FIXTURE_PREFIX:-wpgo249}
port0=${WPGO_FIXTURE_PORT:-22490}
net=$prefix-net db=$prefix-db wp=$prefix-ssh img=$prefix-ssh:fixture
base_images=(wordpress:cli mariadb:11.4)
pulled=()
for i in "${base_images[@]}"; do
	docker image inspect "$i" >/dev/null 2>&1 || pulled+=("$i")
done

teardown() {
	{
		echo "## teardown $(date -u +%FT%TZ)"
		docker rm -f "$wp" "$db"
		docker network rm "$net"
		docker image rm "$img"
		for i in "${pulled[@]}"; do docker image rm "$i"; done
		echo "## remaining $prefix- containers:"
		docker ps -a --filter "name=$prefix-" --format '{{.Names}}'
		echo "## remaining $prefix- networks:"
		docker network ls --filter "name=$prefix-" --format '{{.Name}}'
		echo "## remaining $prefix-ssh images:"
		docker images --filter "reference=$prefix-ssh" --format '{{.Repository}}:{{.Tag}}'
		echo "## end"
	} 2>&1 | tee "$scratch/logs/teardown.log"
}
trap teardown EXIT

# N runs native wp in the fixture with exact argv, as wpuser, with NENV.
NENV=()
N() { docker exec -u wpuser -w /var/www/html "${NENV[@]}" "$wp" wp "$@"; }
Q() { N db query --skip-column-names "$1"; }

setup_fixture() {
	docker rm -f "$wp" "$db" >/dev/null 2>&1
	docker network rm "$net" >/dev/null 2>&1
	docker network create "$net" >/dev/null || exit 1
	docker run -d --name "$db" --network "$net" -e MARIADB_DATABASE=wp \
		-e MARIADB_USER=wp -e MARIADB_PASSWORD=wp -e MARIADB_RANDOM_ROOT_PASSWORD=1 \
		mariadb:11.4 >/dev/null || exit 1
	docker build -q -t "$img" "$here" >/dev/null || exit 1
	local ports=() i=0
	for p in 22 2201 2202 2203 2204 2205; do
		ports+=(-p "127.0.0.1:$((port0 + i)):$p")
		i=$((i + 1))
	done
	docker run -d --name "$wp" --network "$net" "${ports[@]}" "$img" >/dev/null || exit 1
	rm -f "$scratch/client_key" "$scratch/client_key.pub"
	ssh-keygen -q -t ed25519 -N '' -C wp-fixture -f "$scratch/client_key"
	docker cp "$scratch/client_key.pub" "$wp:/etc/ssh/authorized_keys/wpuser"
	docker exec "$wp" sh -c 'chown root:root /etc/ssh/authorized_keys/wpuser && chmod 644 /etc/ssh/authorized_keys/wpuser'
	for _ in $(seq 60); do
		docker exec "$db" healthcheck.sh --connect --innodb_initialized >/dev/null 2>&1 && break
		sleep 1
	done
	N config create --dbname=wp --dbuser=wp --dbpass=wp --dbhost="$db" --skip-check >/dev/null || exit 1
	N core install --url=http://fixture.invalid --title=wp-fixture --admin_user=admin \
		--admin_password=fixture-only --admin_email=admin@fixture.invalid --skip-email >/dev/null || exit 1
	PID=$(N post create --post_status=draft --post_title=wpgo-probe --porcelain)
	# U_NUM's login is a number no user ID has; U_ONE's login is "1",
	# admin's ID.
	U_NUM=$(N user create 4242 u4242@fixture.invalid --porcelain)
	U_ONE=$(N user create 1 one@fixture.invalid --porcelain)
	MISSING_ID=4242

	local h=$scratch/home
	rm -rf "$h"
	mkdir -p "$h/.ssh" "$h/.config/wpgo" "$h/.cache"
	local cfg='' sites='' limits='' sep=''
	i=0
	for s in std wpe wpedrop wpebad stdforce wpeforce; do
		cfg+="Host fx-$s"$'\n'"  HostName 127.0.0.1"$'\n'"  Port $((port0 + i))"$'\n'"  User wpuser"$'\n'"  IdentityFile $scratch/client_key"$'\n'
		local type=standard
		[[ $s == wpe* ]] && type=wpengine
		sites+="$sep\"fx-$s\":{\"wp_path\":\"/var/www/html\",\"host_type\":\"$type\"}"
		limits+="$sep\"127.0.0.1:$((port0 + i))\":{\"delay\":0,\"max_conns\":3}"
		sep=, i=$((i + 1))
	done
	printf '%s' "$cfg" >"$h/.ssh/config"
	printf '{"sites":{%s}}' "$sites" >"$h/.config/wpgo/sites.json"
	printf '{"rate_limits":{%s}}' "$limits" >"$h/.config/wpgo/config.json"
	chmod 600 "$h/.ssh/config"
}

# G runs wpgo against fixture site fx-$1 with HOME in SCRATCH.
G() {
	local site=$1
	shift
	env HOME="$scratch/home" XDG_CONFIG_HOME="$scratch/home/.config" XDG_CACHE_HOME="$scratch/home/.cache" \
		XDG_DATA_HOME="$scratch/home/.local/share" SSH_AUTH_SOCK= "$WPGO" -s "fx-$site" --no-cache "$@"
}

# act ROUTE WORDS... -- ARGS... runs one command on ROUTE: native wp gets
# WORDS ARGS; wpgo gets WORDS -- ARGS so a value may start with "-".
act() {
	local route=$1 words=()
	shift
	while [[ $1 != -- ]]; do
		words+=("$1")
		shift
	done
	shift
	case $route in
	native) N "${words[@]}" "$@" ;;
	native-force)
		NENV=(-e WP_CLI_FORCE_USER_LOGIN=1)
		N "${words[@]}" "$@"
		local rc=$?
		NENV=()
		return $rc
		;;
	expect-refused)
		echo "(no command: the expected result is a refusal that writes nothing)"
		return 1
		;;
	*) G "$route" "${words[@]}" -- "$@" ;;
	esac
}

# shown ROUTE WORDS... -- ARGS... prints the command act runs, quoted.
shown() {
	local route=$1
	shift
	case $route in
	native*)
		local w=() seen=''
		for x in "$@"; do
			[[ -z $seen && $x == -- ]] && seen=1 && continue
			w+=("$x")
		done
		echo "wp ${w[*]@Q}"
		;;
	expect-refused) echo "(none)" ;;
	*) echo "wpgo -s fx-$route ${*@Q}" ;;
	esac
}

# runcase NAME ROUTES runs ACT on each route between SETUP, READ and CLEAN.
verdicts=()
runcase() {
	local name=$1 routes=$2 ref='' refroute='' rb rc v
	for route in $routes; do
		echo "=== $name [$route]"
		$SETUP >/dev/null 2>&1
		echo "--- before"
		$READ 2>&1
		echo "--- command: $route $(shown "$route" "${ACT[@]}")"
		act "$route" "${ACT[@]}" 2>&1
		rc=$?
		echo "[exit $rc]"
		rb="exit=$([[ $rc == 0 ]] && echo 0 || echo nonzero)"$'\n'"$($READ 2>&1)"
		echo "--- read-back"
		printf '%s\n' "$rb"
		echo "--- cleanup"
		$CLEAN 2>&1
		if [[ -z $refroute ]]; then
			ref=$rb refroute=$route
		else
			[[ $rb == "$ref" ]] && v=MATCH || v=DIFF
			verdicts+=("$v  $name  [$route vs $refroute]")
			echo "--- verdict: $v"
		fi
	done
}

pwn_check() { docker exec "$wp" sh -c 'if [ -e /tmp/pwn ]; then echo "PWNED: /tmp/pwn exists"; rm -f /tmp/pwn; else echo "pwn: none"; fi'; }
read_postmeta() {
	N post meta list "$PID" --format=json
	pwn_check
}
read_usermeta() {
	Q "SELECT user_id, meta_key, meta_value FROM wp_usermeta WHERE meta_key LIKE 'wpgo249%' OR meta_key LIKE 'a%b' ORDER BY user_id, meta_key"
	pwn_check
}
read_options() {
	Q "SELECT option_name, option_value, autoload FROM wp_options WHERE option_name LIKE 'wpgo249%' ORDER BY option_name"
	pwn_check
}
clean_postmeta() { Q "DELETE FROM wp_postmeta WHERE post_id=$PID"; }
clean_usermeta() { Q "DELETE FROM wp_usermeta WHERE meta_key LIKE 'wpgo249%' OR meta_key LIKE 'a%b'"; }
clean_options() {
	Q "DELETE FROM wp_options WHERE option_name LIKE 'wpgo249%'"
	docker exec "$wp" rm -f /var/www/html/wp-content/mu-plugins/autoload-off.flag
}
autoload_off() {
	clean_options
	docker exec -u wpuser "$wp" touch /var/www/html/wp-content/mu-plugins/autoload-off.flag
}
opt_old() {
	clean_options
	N option add wpgo249_k v
}
cat_base() { N option update category_base ""; }
read_cat() { N option get category_base --format=json; }
home_reset() { N option update home http://fixture.invalid; }
read_home() { N option get home --format=json; }
pm_ab() {
	clean_postmeta
	N post meta add "$PID" ab old
}
um_ab() {
	clean_usermeta
	N user meta add 1 ab old
}
pm_upper() {
	clean_postmeta
	N post meta add "$PID" wpgo249_upper OLD
}
um_upper() {
	clean_usermeta
	N user meta add 1 wpgo249_upper OLD
}

run_cases() {
	echo "## wpgo: $WPGO ($("$WPGO" version 2>&1 | head -1))"
	echo "## fixture: post $PID, users admin=1 login-4242=$U_NUM login-1=$U_ONE"

	# User identity, through user meta update.
	SETUP=clean_usermeta READ=read_usermeta CLEAN=clean_usermeta
	ACT=(user meta update -- "$MISSING_ID" wpgo249_k v)
	runcase "user: numeric ID $MISSING_ID missing, login $MISSING_ID exists" "native std wpe"
	ACT=(user meta update -- 1 wpgo249_k v)
	runcase "user: numeric ID 1 that is also a login" "native std wpe"
	runcase "user: ID 1 with WP_CLI_FORCE_USER_LOGIN=1" "native-force stdforce wpeforce"
	ACT=(user meta update -- "$MISSING_ID" wpgo249_k v)
	runcase "user: $MISSING_ID with WP_CLI_FORCE_USER_LOGIN=1" "native-force stdforce wpeforce"
	ACT=(user meta update -- one@fixture.invalid wpgo249_k v)
	runcase "user: email" "native std wpe"
	ACT=(user meta update -- admin wpgo249_k v)
	runcase "user: login" "native std wpe"

	# Options.
	SETUP=clean_options READ=read_options CLEAN=clean_options
	ACT=(option add -- wpgo249_k v)
	runcase "option add: autoload" "native std wpe"
	echo "=== control: add_option() without autoload while autoload-off.flag exists"
	autoload_off
	N eval 'add_option( "wpgo249_ctl", "v" );'
	read_options
	clean_options
	SETUP=autoload_off
	runcase "option add: autoload with wp_default_autoload_value false" "native std wpe"
	SETUP=clean_options
	ACT=(option add -- wpgo249_k "")
	runcase "option add: empty value" "native std wpe"
	SETUP=opt_old
	ACT=(option add -- wpgo249_k other)
	runcase "option add: existing option" "native std wpe"
	ACT=(option update -- wpgo249_k v)
	runcase "option update: unchanged" "native std wpe"
	SETUP=cat_base READ=read_cat CLEAN=cat_base
	ACT=(option update -- category_base /news/)
	runcase "option update: category_base trailing slash" "native std wpe"
	SETUP=home_reset READ=read_home CLEAN=home_reset
	ACT=(option update -- home http://fixture.invalid/sub/)
	runcase "option update: home trailing slash" "native std wpe"

	# Meta key escaping and sanitizers.
	SETUP=pm_ab READ=read_postmeta CLEAN=clean_postmeta
	ACT=(post meta update -- "$PID" 'a\b' new)
	runcase "post meta: key a\\b with existing key ab" "native std wpe"
	SETUP=um_ab READ=read_usermeta CLEAN=clean_usermeta
	ACT=(user meta update -- admin 'a\b' new)
	runcase "user meta: key a\\b with existing key ab" "native std wpe"
	SETUP=pm_upper READ=read_postmeta CLEAN=clean_postmeta
	ACT=(post meta update -- "$PID" wpgo249_upper old)
	runcase "post meta: sanitized value equals stored value" "native std wpe"
	SETUP=um_upper READ=read_usermeta CLEAN=clean_usermeta
	ACT=(user meta update -- admin wpgo249_upper old)
	runcase "user meta: sanitized value equals stored value" "native std wpe"

	# Values that a shell or the gateway could split or interpret.
	local values=('a b c' 'x; touch /tmp/pwn' '$(id)' '`id`' "it's" 'say "hi"' 'C:\path\"q"' $'line1\nline2' 'café ☕ 日本' '' '-x' '--x' '--autoload=no' '--quiet' '-')
	for value in "${values[@]}"; do
		local label=${value//$'\n'/\\n}
		SETUP=clean_postmeta READ=read_postmeta CLEAN=clean_postmeta
		ACT=(post meta update -- "$PID" wpgo249_k "$value")
		runcase "value '$label': post meta update" "native std wpe"
		SETUP=clean_usermeta READ=read_usermeta CLEAN=clean_usermeta
		ACT=(user meta update -- admin wpgo249_k "$value")
		runcase "value '$label': user meta update" "native std wpe"
		SETUP=opt_old READ=read_options CLEAN=clean_options
		ACT=(option update -- wpgo249_k "$value")
		runcase "value '$label': option update" "native std wpe"
		SETUP=clean_options
		ACT=(option add -- wpgo249_k "$value")
		runcase "value '$label': option add" "native std wpe"
	done

	# A gateway that drops or replaces the last word must not write.
	SETUP=opt_old READ=read_options CLEAN=clean_options
	ACT=(option update -- wpgo249_k new)
	runcase "damaged arguments: option update" "expect-refused wpedrop wpebad"

	echo "=== issue #249 re-check shape: post meta update <draft> _fox_wpgo_probe 'a b c', get, delete"
	for route in std wpe; do
		echo "--- [$route]"
		G "$route" post meta update -- "$PID" _fox_wpgo_probe 'a b c' 2>&1
		echo "[exit $?]"
		G "$route" post meta get -- "$PID" _fox_wpgo_probe 2>&1
		echo "[exit $?]"
		G "$route" post meta delete -- "$PID" _fox_wpgo_probe 2>&1
		echo "[exit $?]"
		echo "read-back after delete:"
		read_postmeta
	done

	echo "=== db query (issue #249 comment)"
	for sql in 'SELECT COUNT(*) AS n FROM wp_options WHERE 1=0' 'SELECT 1 AS one'; do
		echo "--- [native] $sql"
		N db query "$sql" 2>&1
		echo "[exit $?]"
		for route in std wpe; do
			echo "--- [$route] $sql"
			G "$route" db query -- "$sql" 2>&1
			echo "[exit $?]"
		done
	done

	echo "=== verdicts"
	printf '%s\n' "${verdicts[@]}"
}

setup_fixture
{
	echo "## fixture versions"
	N cli version
	N core version
	docker exec "$wp" php -r 'echo "PHP ", PHP_VERSION, "\n";'
	Q 'SELECT VERSION()'
} 2>&1 | tee "$scratch/logs/versions.log"
for spec in "$@"; do
	label=${spec%%=*} WPGO=${spec#*=}
	verdicts=()
	run_cases 2>&1 | tee "$scratch/logs/$label.log"
done
