ObjC.import("AppKit");

function run() {
    // read stdin and parse as JSON
    const data = $.NSFileHandle.fileHandleWithStandardInput.readDataToEndOfFile;
    const text = $.NSString.alloc.initWithDataEncoding(data, $.NSUTF8StringEncoding);
    const request = JSON.parse(ObjC.unwrap(text));

    const app = $.NSApplication.sharedApplication;
    app.setActivationPolicy($.NSApplicationActivationPolicyAccessory);

    const dialog = $.NSAlert.alloc.init;
    dialog.messageText = "Pact approval";
    dialog.informativeText = request.summary;

    for (const { name, title } of request.buttons) {
        const button = dialog.addButtonWithTitle(title);
        button.keyEquivalent = "";
        button.identifier = name;

        if (name === "deny") {
          button.keyEquivalent = "\u001b";
          dialog.window.defaultButtonCell = button.cell;
        }
    }

    const scroll = $.NSScrollView.alloc.initWithFrame($.NSMakeRect(0, 0, 640, 320));
    scroll.hasVerticalScroller = true;

    const details = $.NSTextView.alloc.initWithFrame($.NSMakeRect(0, 0, 620, 320));
    details.editable = false;
    details.font = $.NSFont.userFixedPitchFontOfSize(12);
    details.string = request.details;
    details.horizontallyResizable = false;
    details.autoresizingMask = $.NSViewWidthSizable;
    details.textContainer.widthTracksTextView = true;

    scroll.documentView = details;
    dialog.accessoryView = scroll;

    app.activateIgnoringOtherApps(true);

    const response = dialog.runModal;
    const buttonIndex = response - $.NSAlertFirstButtonReturn;
    if (buttonIndex >= 0 && buttonIndex < request.buttons.length) {
      return request.buttons[buttonIndex].name;
    } else {
      return "deny"
    }
}
