#import <Cocoa/Cocoa.h>
#import <WebKit/WebKit.h>

extern void goOpenWindow(void);
extern void goQuit(void);

static NSString *const kAppURL = @"http://127.0.0.1:8090/app";

@interface HajirDelegate : NSObject <NSApplicationDelegate, WKUIDelegate, WKNavigationDelegate>
@property (strong) NSStatusItem *item;
@property (strong) NSPopover *popover;
@property (strong) WKWebView *web;
@property (strong) NSMenu *menu;
@end

@implementation HajirDelegate

- (void)setupHidden:(BOOL)hidden {
    // Managed/covert device: keep the WebView + delegate for control, but do not
    // place a menu-bar item and never auto-show the popover.
    if (!hidden) {
    self.item = [[NSStatusBar systemStatusBar] statusItemWithLength:NSVariableStatusItemLength];
    NSImage *img = nil;
    NSString *logo = [[NSBundle mainBundle] pathForResource:@"hajir-menubar" ofType:@"png"];
    if (logo) {
        img = [[NSImage alloc] initWithContentsOfFile:logo];
        if (img) img.size = NSMakeSize(18, 18);
    }
    if (!img) if (@available(macOS 11.0, *)) {
        img = [NSImage imageWithSystemSymbolName:@"timer" accessibilityDescription:@"Hajir Tracker"];
    }
    if (img) {
        img.template = NO;
        self.item.button.image = img;
        self.item.button.imagePosition = NSImageOnly;
    }
    self.item.button.title = @"";
    self.item.button.toolTip = @"Hajir Tracker";
    self.item.button.target = self;
    self.item.button.action = @selector(statusClicked:);
    [self.item.button sendActionOn:(NSEventMaskLeftMouseUp | NSEventMaskRightMouseUp)];
    }

    self.menu = [[NSMenu alloc] init];
    NSMenuItem *open = [[NSMenuItem alloc] initWithTitle:@"Open Hajir Tracker" action:@selector(showPopover:) keyEquivalent:@""];
    open.target = self;
    [self.menu addItem:open];
    NSMenuItem *win = [[NSMenuItem alloc] initWithTitle:@"Open in Separate Window" action:@selector(openWindow:) keyEquivalent:@""];
    win.target = self;
    [self.menu addItem:win];
    [self.menu addItem:[NSMenuItem separatorItem]];
    NSMenuItem *quit = [[NSMenuItem alloc] initWithTitle:@"Quit Hajir Tracker" action:@selector(quitApp:) keyEquivalent:@"q"];
    quit.target = self;
    [self.menu addItem:quit];

    WKWebViewConfiguration *cfg = [[WKWebViewConfiguration alloc] init];
    self.web = [[WKWebView alloc] initWithFrame:NSMakeRect(0, 0, 320, 470) configuration:cfg];
    self.web.UIDelegate = self;
    self.web.navigationDelegate = self;

    NSViewController *vc = [[NSViewController alloc] init];
    vc.view = self.web;
    self.popover = [[NSPopover alloc] init];
    self.popover.contentViewController = vc;
    self.popover.contentSize = NSMakeSize(320, 470);
    self.popover.behavior = NSPopoverBehaviorTransient;
    self.popover.animates = YES;

    [self loadApp];
}

- (void)loadApp {
    [self.web loadRequest:[NSURLRequest requestWithURL:[NSURL URLWithString:kAppURL]]];
}

- (void)statusClicked:(id)sender {
    NSEvent *event = [NSApp currentEvent];
    if (event.type == NSEventTypeRightMouseUp || (event.modifierFlags & NSEventModifierFlagControl)) {
        [self.popover performClose:nil];
        self.item.menu = self.menu;
        [self.item.button performClick:nil];
        self.item.menu = nil;
        return;
    }
    if (self.popover.shown) {
        [self.popover performClose:nil];
    } else {
        [self showPopover:nil];
    }
}

- (void)showPopover:(id)sender {
    if (self.item == nil) {
        return; // managed/covert device: no menu-bar anchor, stay hidden
    }
    if (self.web.URL == nil) {
        [self loadApp];
    }
    [NSApp activateIgnoringOtherApps:YES];
    [self.popover showRelativeToRect:self.item.button.bounds ofView:self.item.button preferredEdge:NSRectEdgeMinY];
    [self.web evaluateJavaScript:@"if (window.hajirRefresh) { window.hajirRefresh(); }" completionHandler:nil];
}

- (void)openWindow:(id)sender { goOpenWindow(); }
- (void)quitApp:(id)sender { goQuit(); }

- (BOOL)applicationShouldHandleReopen:(NSApplication *)sender hasVisibleWindows:(BOOL)flag {
    [self showPopover:nil];
    return NO;
}

- (void)webView:(WKWebView *)webView didFailProvisionalNavigation:(WKNavigation *)navigation withError:(NSError *)error {
    dispatch_after(dispatch_time(DISPATCH_TIME_NOW, (int64_t)(1 * NSEC_PER_SEC)), dispatch_get_main_queue(), ^{ [self loadApp]; });
}

- (void)webView:(WKWebView *)webView didFailNavigation:(WKNavigation *)navigation withError:(NSError *)error {
    dispatch_after(dispatch_time(DISPATCH_TIME_NOW, (int64_t)(1 * NSEC_PER_SEC)), dispatch_get_main_queue(), ^{ [self loadApp]; });
}

- (void)webView:(WKWebView *)webView runJavaScriptAlertPanelWithMessage:(NSString *)message initiatedByFrame:(WKFrameInfo *)frame completionHandler:(void (^)(void))completionHandler {
    NSAlert *alert = [[NSAlert alloc] init];
    alert.messageText = @"Hajir Tracker";
    alert.informativeText = message;
    [alert runModal];
    completionHandler();
}

- (void)webView:(WKWebView *)webView runJavaScriptConfirmPanelWithMessage:(NSString *)message initiatedByFrame:(WKFrameInfo *)frame completionHandler:(void (^)(BOOL))completionHandler {
    NSAlert *alert = [[NSAlert alloc] init];
    alert.messageText = @"Hajir Tracker";
    alert.informativeText = message;
    [alert addButtonWithTitle:@"OK"];
    [alert addButtonWithTitle:@"Cancel"];
    completionHandler([alert runModal] == NSAlertFirstButtonReturn);
}

- (WKWebView *)webView:(WKWebView *)webView createWebViewWithConfiguration:(WKWebViewConfiguration *)configuration forNavigationAction:(WKNavigationAction *)navigationAction windowFeatures:(WKWindowFeatures *)windowFeatures {
    if (navigationAction.request.URL) {
        [[NSWorkspace sharedWorkspace] openURL:navigationAction.request.URL];
    }
    return nil;
}

- (void)webViewDidClose:(WKWebView *)webView {
    [self.popover performClose:nil];
}

@end

static HajirDelegate *hajirDelegate;

void hideStatusItem(void) {
    dispatch_async(dispatch_get_main_queue(), ^{
        if (hajirDelegate) {
            if (hajirDelegate.popover.shown) {
                [hajirDelegate.popover performClose:nil];
            }
            if (hajirDelegate.item) {
                [[NSStatusBar systemStatusBar] removeStatusItem:hajirDelegate.item];
                hajirDelegate.item = nil;
            }
        }
    });
}

void runCocoa(int hidden) {
    @autoreleasepool {
        [NSApplication sharedApplication];
        [NSApp setActivationPolicy:NSApplicationActivationPolicyAccessory];
        hajirDelegate = [[HajirDelegate alloc] init];
        [NSApp setDelegate:hajirDelegate];
        [hajirDelegate setupHidden:(hidden ? YES : NO)];
        if (!hidden) {
            dispatch_after(dispatch_time(DISPATCH_TIME_NOW, (int64_t)(1.5 * NSEC_PER_SEC)), dispatch_get_main_queue(), ^{
                [hajirDelegate showPopover:nil];
            });
        }
        [NSApp run];
    }
}
