<?php
// Fixture sanitizers: meta key wpgo249_upper is stored uppercased.
add_filter( 'sanitize_post_meta_wpgo249_upper', 'strtoupper' );
add_filter( 'sanitize_user_meta_wpgo249_upper', 'strtoupper' );
// While autoload-off.flag exists, new options default to autoload off, as a
// site filter can on WordPress 6.6+.
if ( file_exists( __DIR__ . '/autoload-off.flag' ) ) {
	add_filter( 'wp_default_autoload_value', '__return_false' );
}
