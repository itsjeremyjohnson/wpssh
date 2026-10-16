<?php
// Runs an eval-file script as `wp eval-file - <args>` would, against stubs of
// the WordPress and WP-CLI functions the scripts call. Usage:
//
//   CALLS_FILE=calls.json php eval_file_harness.php script.php arg...
//
// Each write is recorded as [function, key, value] with the key and value as
// WordPress would store them, and the list is written to CALLS_FILE as JSON
// on exit. Post 1 and user "admin" exist; every existing value is "old".

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
function record( $function, $key, $value ) {
	global $calls;
	$calls[] = array( $function, $key, $value );
	return true;
}

function get_post( $id ) {
	return 1 === $id ? (object) array( 'ID' => 1 ) : null;
}
function get_post_meta( $id, $key, $single ) {
	return 'old';
}
function get_user_meta( $id, $key, $single ) {
	return 'old';
}
function is_email( $user ) {
	return false !== strpos( $user, '@' );
}
function get_user_by( $field, $value ) {
	return 'login' === $field && 'admin' === $value ? (object) array( 'ID' => 7 ) : false;
}
function get_option( $key ) {
	return 'old';
}
function sanitize_option( $key, $value ) {
	return $value;
}

// Meta writes unslash their key and value, as update_metadata does.
function update_post_meta( $id, $key, $value ) {
	return 1 === $id && record( __FUNCTION__, wp_unslash( $key ), wp_unslash( $value ) );
}
function update_user_meta( $id, $key, $value ) {
	return 7 === $id && record( __FUNCTION__, wp_unslash( $key ), wp_unslash( $value ) );
}
// Option writes store their value as given.
function update_option( $key, $value ) {
	return record( __FUNCTION__, $key, $value );
}
function add_option( $key, $value ) {
	return record( __FUNCTION__, $key, $value );
}

( function ( $args ) {
	include $GLOBALS['argv'][1];
} )( array_slice( $argv, 2 ) );
