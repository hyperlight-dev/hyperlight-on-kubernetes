// Hyperlight Host Application
// Runs the Hyperlight guest inside a micro-VM

use std::path::{Path, PathBuf};

use anyhow::Result;
use clap::Parser;
use hyperlight_host::{GuestBinary, MultiUseSandbox, UninitializedSandbox};

#[derive(Parser, Debug)]
#[command(name = "hyperlight-hello")]
#[command(about = "Hyperlight demo application running on Kubernetes")]
struct Args {
    /// Path to the guest binary
    #[arg(short, long, default_value = "/app/guest/hyperlight-hello-guest")]
    guest: PathBuf,

    /// Message to echo
    #[arg(short, long, default_value = "Hello from Kubernetes!")]
    message: String,

    /// Run in loop mode (for deployment)
    #[arg(short, long)]
    loop_mode: bool,
}

fn fn_print(msg: String) -> hyperlight_host::Result<i32> {
    print!("{}", msg);
    Ok(msg.len() as i32)
}

fn run_demo(guest_path: &Path, message: &str) -> Result<()> {
    println!("=== Hyperlight on Kubernetes Demo ===\n");

    // Check hypervisor
    let hypervisor = if Path::new("/dev/mshv").exists() {
        "MSHV"
    } else if Path::new("/dev/kvm").exists() {
        "KVM"
    } else {
        "Unknown"
    };
    println!("Hypervisor: {}", hypervisor);
    println!("Guest binary: {}\n", guest_path.display());

    // Create sandbox
    let mut uninit = UninitializedSandbox::new(
        GuestBinary::FilePath(guest_path.to_string_lossy().to_string()),
        None,
    )?;

    // Register host print function
    uninit.register_print(fn_print)?;

    // Initialize sandbox
    let mut sandbox: MultiUseSandbox = uninit.evolve()?;

    // Call Echo function
    println!("--- Testing Echo ---");
    let result: String = sandbox.call("Echo", message.to_string())?;
    println!("Echo result: {}\n", result);

    // Call PrintOutput function
    println!("--- Testing PrintOutput ---");
    let result: i32 = sandbox.call(
        "PrintOutput",
        "Hello, World! I am executing inside a VM :)\n".to_string(),
    )?;
    println!("PrintOutput returned: {}\n", result);

    println!("=== Demo Complete ===");
    Ok(())
}

fn main() -> Result<()> {
    let args = Args::parse();

    if args.loop_mode {
        println!("Running in loop mode for Kubernetes deployment...");
        loop {
            if let Err(e) = run_demo(&args.guest, &args.message) {
                eprintln!("Error running demo: {}", e);
            }
            std::thread::sleep(std::time::Duration::from_secs(30));
        }
    } else {
        run_demo(&args.guest, &args.message)
    }
}
