#![no_std]
#![no_main]
extern crate alloc;

use alloc::string::String;
use alloc::vec::Vec;
use hyperlight_common::flatbuffer_wrappers::function_call::FunctionCall;
use hyperlight_common::flatbuffer_wrappers::guest_error::ErrorCode;

use hyperlight_guest::bail;
use hyperlight_guest::error::Result;
use hyperlight_guest_bin::{guest_function, host_function};

#[host_function("HostPrint")]
fn host_print(message: String) -> Result<i32>;

#[guest_function("PrintOutput")]
fn print_output(message: String) -> Result<i32> {
    let result = host_print(message)?;
    Ok(result)
}

#[guest_function("Echo")]
fn echo(value: String) -> String {
    value
}

#[no_mangle]
pub extern "C" fn hyperlight_main() {
    // initialization code
}

#[no_mangle]
pub fn guest_dispatch_function(function_call: FunctionCall) -> Result<Vec<u8>> {
    let function_name = function_call.function_name;
    bail!(ErrorCode::GuestFunctionNotFound => "{function_name}");
}
