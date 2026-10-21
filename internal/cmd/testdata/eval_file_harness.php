<?php
// Runs an eval-file script as `wp eval-file - <args>` would, against stubs of
// the WordPress and WP-CLI functions the scripts call. Usage:
//
//   CALLS_FILE=calls.json php eval_file_harness.php script.php arg...
//
// Each write is recorded as [function, object ID, key, value] with the key and
// value as WordPress would store them (object ID null for options), and the
// list is written to CALLS_FILE as JSON on exit.
//
// Post 1 exists. Users 7, 8 and 9 have logins "admin", "123" and "7". Meta
// key "upper" has a sanitizer that uppercases values and holds "OLD"; every
// other existing value is "old".

$calls = array();
register_shutdown_function(
	function () {
		global $calls;
		file_put_contents( getenv( 'CALLS_FILE' ), json_encode( $calls ) );
	}
);

class WP_CLI {
	public static function success( $message ) {
		echo "Success: $message\n";
	}
	public static function warning( $message ) {
		fwrite( STDERR, "Warning: $message\n" );
	}
	public static function error( $message ) {
		fwrite( STDERR, "Error: $message\n" );
		exit( 1 );
	}
}

function wp_slash( $value ) {
	return is_string( $value ) ? addslashes( $value ) : $value;
}
function wp_unslash( $value ) {
	return is_string( $value ) ? stripslashes( $value ) : $value;
}
function untrailingslashit( $value ) {
	return rtrim( $value, '/\\' );
}
function record( $function, $id, $key, $value ) {
	global $calls;
	$calls[] = array( $function, $id, $key, $value );
	return true;
}

function get_post( $id ) {
	return 1 === $id ? (object) array( 'ID' => 1 ) : null;
}
function get_post_meta( $id, $key, $single ) {
	return 'upper' === $key ? 'OLD' : 'old';
}
function get_user_meta( $id, $key, $single ) {
	return get_post_meta( $id, $key, $single );
}
function sanitize_meta( $key, $value, $type ) {
	return 'upper' === $key ? strtoupper( $value ) : $value;
}
function is_email( $user ) {
	return false !== strpos( $user, '@' );
}
function get_user_by( $field, $value ) {
	$logins = array( 7 => 'admin', 8 => '123', 9 => '7' );
	if ( 'id' === $field ) {
		$id = is_numeric( $value ) ? (int) $value : 0;
	} elseif ( 'login' === $field ) {
		$id = array_search( (string) $value, $logins, true );
	} else {
		$id = false;
	}
	return isset( $logins[ $id ] ) ? (object) array( 'ID' => $id ) : false;
}
function get_option( $key ) {
	return 'old';
}
function sanitize_option( $key, $value ) {
	return $value;
}

// Meta writes unslash and sanitize their value and return false when it
// equals the stored value, as update_metadata does.
function update_meta( $function, $id, $key, $value ) {
	$key   = wp_unslash( $key );
	$value = sanitize_meta( $key, wp_unslash( $value ), 'post' );
	return $value !== get_post_meta( $id, $key, true ) && record( $function, $id, $key, $value );
}
function update_post_meta( $id, $key, $value ) {
	return 1 === $id && update_meta( __FUNCTION__, $id, $key, $value );
}
function update_user_meta( $id, $key, $value ) {
	return update_meta( __FUNCTION__, $id, $key, $value );
}
// Option writes store their value as given.
function update_option( $key, $value ) {
	return record( __FUNCTION__, null, $key, $value );
}
function add_option( $key, $value ) {
	return record( __FUNCTION__, null, $key, $value );
}

( function ( $args ) {
	include $GLOBALS['argv'][1];
} )( array_slice( $argv, 2 ) );
