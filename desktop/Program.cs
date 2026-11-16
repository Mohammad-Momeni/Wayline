using System.Diagnostics;
using System.IO;
using System.Security.Principal;
using System.Text.Json;
using System.Windows;
using System.Windows.Controls;
using System.Windows.Media;
using System.Windows.Media.Imaging;
using System.Windows.Threading;
using Microsoft.Win32;

namespace Wayline;

public static class Program
{
    [STAThread]
    public static void Main()
    {
        if (Environment.GetCommandLineArgs().Contains("--self-test")) { SelfTest.Run(); return; }
        var app = new Application();
        AppTheme.Load();
        app.Resources = AppTheme.Resources();
        app.DispatcherUnhandledException += (_, e) => { try { File.AppendAllText(Path.Combine(AppContext.BaseDirectory, "desktop-error.txt"), e.Exception.ToString()); } catch { } MessageBox.Show(e.Exception.Message, "Wayline", MessageBoxButton.OK, MessageBoxImage.Error); e.Handled = true; };
        var window = new MainWindow();
        var args = Environment.GetCommandLineArgs();
        if (args.Contains("--startup")) window.WindowState = WindowState.Minimized;
        int preview = Array.IndexOf(args, "--preview");
        if (preview >= 0 && preview + 1 < args.Length)
        {
            if (preview + 3 < args.Length && int.TryParse(args[preview + 2], out int width) && int.TryParse(args[preview + 3], out int height))
            { window.Width = width; window.Height = height; }
            int pageIndex=Array.IndexOf(args,"--preview-page");if(pageIndex>=0&&pageIndex+1<args.Length)window.SelectPage(args[pageIndex+1]);
            window.Loaded += (_, _) => window.Dispatcher.BeginInvoke(DispatcherPriority.ContextIdle, new System.Action(() =>
            {
                window.UpdateLayout();
                var image = new RenderTargetBitmap((int)window.ActualWidth, (int)window.ActualHeight, 96, 96, PixelFormats.Pbgra32);
                image.Render(window);
                var encoder = new PngBitmapEncoder(); encoder.Frames.Add(BitmapFrame.Create(image));
                using (var stream = File.Create(args[preview + 1])) encoder.Save(stream);
                window.Close();
            }));
        }
        app.Run(window);
    }
}

public sealed class MainWindow : Window
{
    readonly ComboBox profiles = new();
    readonly TextBox name = new(), gateway = new(), port = new(), user = new(), realm = new(), cert = new(), listen = new(), dns = new();
    readonly PasswordBox password = new();
    readonly CheckBox remember = new() { Content = "Remember password in Windows Credential Manager" };
    readonly CheckBox reconnect = new() { Content = "Auto reconnect after connection loss" };
    readonly StackPanel xrayLibrary = new();
    readonly ComboBox appFilter=new(){ItemsSource=new[]{"All apps","Only selected apps","Exclude selected apps"}};
    readonly TextBlock appFilterSummary=new(){Foreground=AppTheme.Muted};
    readonly ComboBox mode = new() { ItemsSource = new[] { "Proxy only", "VPN · system traffic" } };
    readonly ListBox before = new(), after = new();
    readonly TextBlock xrayLabel = new() { Foreground = AppTheme.Muted, VerticalAlignment = VerticalAlignment.Center, TextWrapping = TextWrapping.Wrap };
    readonly TextBlock status = new() { Text = "DISCONNECTED", FontWeight = FontWeights.SemiBold }, flow = new() { TextWrapping = TextWrapping.Wrap }, explanation = new() { TextWrapping = TextWrapping.Wrap };
    readonly TextBox logs = new() { IsReadOnly = true, AcceptsReturn = true, VerticalScrollBarVisibility = ScrollBarVisibility.Auto, FontFamily = new FontFamily("Consolas"), FontSize = 12, MinHeight = 150 };
    readonly Button connect = new() { Content = "Connect", MinWidth = 120 }, disconnect = new() { Content = "Disconnect", IsEnabled = false };
    readonly List<Profile> data;
    readonly TabControl navigation=new();
    readonly StackPanel editor = new(), chainEditor = new(), xrayEditor = new();
    string? onceFingerprint;
    bool retryCertificate;
    readonly StackPanel sidebar = new() { Width = 175, Margin = new Thickness(0, 0, 24, 0) };
    Profile? current;
    Process? engine;
    bool loading;
    bool closing;
    string? lastFingerprint;

    static Brush Ink => AppTheme.Text;
    static Brush Accent => AppTheme.Accent;
    public MainWindow()
    {
        string version = typeof(Program).Assembly.GetName().Version!.ToString(3);
        Title = "Wayline " + version; Width = Math.Min(1040, SystemParameters.WorkArea.Width - 32); Height = Math.Min(800, SystemParameters.WorkArea.Height - 40); MinWidth = 680; MinHeight = 480;
        Background = AppTheme.Background; Foreground = Ink;
        Icon = BitmapFrame.Create(new Uri("pack://application:,,,/Assets/wayline.png"));
        FontFamily = new FontFamily("Segoe UI"); FontSize = 14; WindowStartupLocation = WindowStartupLocation.CenterScreen;
        AddStyles();flow.Foreground=Ink;explanation.Foreground=AppTheme.Muted;
        var shell = new DockPanel { Margin = new Thickness(16) };
        var header = new DockPanel { Margin = new Thickness(0, 0, 0, 20) };
        var appIcon = new Image { Source = Icon, Width = 42, Height = 42, Margin = new Thickness(0, 0, 12, 0) };
        header.Children.Add(appIcon);
        var logo = new StackPanel(); logo.Children.Add(new TextBlock { Text = "Wayline", FontSize = 27, FontWeight = FontWeights.Bold, Foreground = Accent });
        logo.Children.Add(new TextBlock { Text = "VPN · Version " + version, Foreground = AppTheme.Muted, Margin = new Thickness(0, 4, 0, 0) });
        header.Children.Add(logo); status.HorizontalAlignment = HorizontalAlignment.Right; status.VerticalAlignment = VerticalAlignment.Center; header.Children.Add(status);
        DockPanel.SetDock(header, Dock.Top); shell.Children.Add(header);
        sidebar.Children.Add(Heading("Connections")); sidebar.Children.Add(profiles);
        sidebar.Children.Add(Action("＋ New profile", NewProfile));
        sidebar.Children.Add(Action("Rename profile", () => Try(RenameProfile)));
        sidebar.Children.Add(Action("Save profile", () => Try(SaveCurrent)));
        sidebar.Children.Add(Action(AppTheme.Dark ? "☀ Light mode" : "☾ Dark mode", () => Try(() => {
            if (engine != null) throw new InvalidOperationException("Disconnect before changing the theme.");
            SaveCurrent();
            AppTheme.Toggle();
            Application.Current.Resources = AppTheme.Resources();
            var replacement = new MainWindow();
            replacement.password.Password = password.Password;
            Application.Current.MainWindow = replacement;
            replacement.Show();
            Close();
        })));
        sidebar.Children.Add(Action("Delete profile", DeleteProfile));
        var startup = new CheckBox { Content = "Start with Windows", IsChecked = WindowsStartup.Enabled, Margin = new Thickness(0, 10, 0, 0) };
        startup.Click += (_, _) => Try(() => WindowsStartup.SetEnabled(startup.IsChecked == true));
        sidebar.Children.Add(startup);
        sidebar.Children.Add(new Separator { Margin = new Thickness(0, 16, 0, 16) });
        sidebar.Children.Add(Action("Import profile…", Import)); sidebar.Children.Add(Action("Export profile…", Export));
        sidebar.Children.Add(new TextBlock { Text = "Profiles stay on this computer. Exported files contain no passwords.", TextWrapping = TextWrapping.Wrap, Foreground = AppTheme.Muted, Margin = new Thickness(4, 12, 4, 0), FontSize = 12 });
        sidebar.Children.Add(new Separator { Margin = new Thickness(0, 20, 0, 16) });
        sidebar.Children.Add(new TextBlock { Text = "FORTINET SSL-VPN\nPassword only · no MFA / SAML\nIPv4 tunnel", TextWrapping = TextWrapping.Wrap, Foreground = AppTheme.Muted, FontSize = 12 });
        sidebar.Margin = new Thickness(0, 0, 12, 0);
        var sidebarScroll = new ScrollViewer { Content = sidebar, Width = 195, VerticalScrollBarVisibility = ScrollBarVisibility.Auto, HorizontalScrollBarVisibility = ScrollBarVisibility.Disabled };
        DockPanel.SetDock(sidebarScroll, Dock.Left); shell.Children.Add(sidebarScroll);
        var tabs=navigation;tabs.Background=AppTheme.Background;tabs.Foreground=Ink;tabs.BorderThickness=new Thickness(0);
        void Page(string title, FrameworkElement content) { tabs.Items.Add(new TabItem { Header = title, Foreground=Ink,Background=AppTheme.Panel,Padding = new Thickness(16, 10, 16, 10), Content = new ScrollViewer { Content = content, VerticalScrollBarVisibility = ScrollBarVisibility.Auto, Padding = new Thickness(16) } }); }
        editor.Children.Add(Heading("Connection"));
        editor.Children.Add(Row(Field("Profile name", name), Field("Traffic mode", mode)));
        editor.Children.Add(Row(Field("Gateway", gateway), Field("Port", port)));
        editor.Children.Add(Row(Field("Username", user), Field("Password", password)));
        editor.Children.Add(remember);editor.Children.Add(reconnect);
        editor.Children.Add(Field("Apps using the VPN",appFilter));editor.Children.Add(appFilterSummary);editor.Children.Add(Action("Choose apps",()=>Try(ChooseApps)));editor.Children.Add(new TextBlock {Text="VPN mode only. Shared system DNS still uses the VPN. Choose executables that make network connections, including any helper processes.",TextWrapping=TextWrapping.Wrap,FontSize=12,Foreground=AppTheme.Muted});
        var advanced = new Expander { Header = "Advanced options", Margin = new Thickness(0, 20, 0, 12) };
        var options = new StackPanel();options.Children.Add(Row(Field("Realm", realm), Field("Local proxy port", listen)));
        options.Children.Add(Field("Trusted certificate SHA-256", cert));options.Children.Add(Action("Manage trusted fingerprints", ManageCertificates));options.Children.Add(Field("DNS override · IPv4 addresses", dns));advanced.Content=options;editor.Children.Add(advanced);Page("Connection",editor);
        chainEditor.Children.Add(Heading("Traffic chain"));chainEditor.Children.Add(new TextBlock { Text="Enabled hops run from top to bottom. Add saved configurations from the Xrays page.",TextWrapping=TextWrapping.Wrap,Foreground=AppTheme.Muted });
        chainEditor.Children.Add(ChainPanel("Before VPN · entry",before,true));chainEditor.Children.Add(ChainPanel("After VPN · exit",after,false));
        chainEditor.Children.Add(new Border { Background=AppTheme.Highlight,CornerRadius=new CornerRadius(12),Padding=new Thickness(14),Child=flow,Margin=new Thickness(0,16,0,0) });chainEditor.Children.Add(explanation);Page("Chain",chainEditor);
        xrayEditor.Children.Add(Heading("Your Xrays"));xrayEditor.Children.Add(new TextBlock { Text="Named configurations, their settings, and exactly where they are used.",Foreground=AppTheme.Muted });
        var imports=new WrapPanel { Margin=new Thickness(0,12,0,12) };imports.Children.Add(Action("Import JSON file",ConfigureXray));imports.Children.Add(Action("Paste from clipboard",()=>Try(()=>ConfigureXrayText(Clipboard.ContainsText()?Clipboard.GetText():""))));imports.Children.Add(Action("Share link",()=>Try(()=>ConfigureXrayText(""))));xrayEditor.Children.Add(imports);xrayEditor.Children.Add(xrayLabel);xrayEditor.Children.Add(xrayLibrary);Page("Xrays",xrayEditor);
        var activity=new DockPanel();var logActions=new WrapPanel();logActions.Children.Add(Action("Copy logs",()=>Clipboard.SetText(logs.Text)));logActions.Children.Add(Action("Clear logs",()=>logs.Clear()));logActions.Children.Add(Action("Copy fingerprint",()=>{if(lastFingerprint!=null)Clipboard.SetText(lastFingerprint);}));DockPanel.SetDock(logActions,Dock.Top);activity.Children.Add(logActions);logs.MinHeight=300;activity.Children.Add(logs);Page("Logs",activity);
        var controls=new StackPanel { Orientation=Orientation.Horizontal,Margin=new Thickness(0,14,0,0) };connect.Background=Accent;connect.Foreground=AppTheme.Background;connect.FontWeight=FontWeights.SemiBold;connect.Click+=async(_,_)=>await Connect();disconnect.Click+=async(_,_)=>await Stop();controls.Children.Add(connect);controls.Children.Add(disconnect);DockPanel.SetDock(controls,Dock.Bottom);shell.Children.Add(controls);shell.Children.Add(tabs);Content=shell;
        data = Store.Load(); if (data.Count == 0) data.Add(new Profile());
        profiles.ItemsSource = data; profiles.SelectionChanged += (_, _) => { if (profiles.SelectedItem is Profile p) LoadProfile(p); };
        profiles.SelectedIndex = 0;
        mode.SelectionChanged += (_, _) => UpdateFlow(); gateway.TextChanged += (_, _) => UpdateFlow(); listen.TextChanged += (_, _) => UpdateFlow();
        Closing += (_, e) => { if (engine != null && !closing) { e.Cancel = true; closing = true; _ = CloseAfterStop(); } };
    }

    public void SelectPage(string title){foreach(TabItem page in navigation.Items)if((string)page.Header==title)navigation.SelectedItem=page;}
    void AddStyles()
    {
        var text = new Style(typeof(TextBox), (Style)Application.Current.Resources[typeof(TextBox)]); text.Setters.Add(new Setter(Control.PaddingProperty, new Thickness(9, 7, 9, 7))); text.Setters.Add(new Setter(Control.BorderBrushProperty, AppTheme.Border)); text.Setters.Add(new Setter(Control.BorderThicknessProperty, new Thickness(1))); Resources.Add(typeof(TextBox), text);
        var pb = new Style(typeof(PasswordBox), (Style)Application.Current.Resources[typeof(PasswordBox)]); pb.Setters.Add(new Setter(Control.PaddingProperty, new Thickness(9, 7, 9, 7))); Resources.Add(typeof(PasswordBox), pb);
        var button = new Style(typeof(Button), (Style)Application.Current.Resources[typeof(Button)]); button.Setters.Add(new Setter(Control.PaddingProperty, new Thickness(13, 8, 13, 8))); button.Setters.Add(new Setter(FrameworkElement.MarginProperty, new Thickness(0, 4, 8, 4))); button.Setters.Add(new Setter(Control.BackgroundProperty, AppTheme.Panel)); button.Setters.Add(new Setter(Control.BorderBrushProperty, AppTheme.Border)); Resources.Add(typeof(Button), button);
        var combo = new Style(typeof(ComboBox), (Style)Application.Current.Resources[typeof(ComboBox)]); combo.Setters.Add(new Setter(Control.PaddingProperty, new Thickness(8))); combo.Setters.Add(new Setter(FrameworkElement.MinHeightProperty, 35.0)); Resources.Add(typeof(ComboBox), combo);
    }
    static TextBlock Heading(string text) => new() { Text = text, FontSize = 16, Foreground=AppTheme.Text,FontWeight = FontWeights.SemiBold, Margin = new Thickness(0, 8, 0, 12) };
    static FrameworkElement Field(string text, Control control)
    {
        var p = new StackPanel { Margin = new Thickness(0, 0, 12, 12) }; p.Children.Add(new TextBlock { Text = text, FontSize = 12, Foreground = AppTheme.Muted, Margin = new Thickness(0, 0, 0, 5) }); p.Children.Add(control); return p;
    }
    static Grid Row(FrameworkElement a, FrameworkElement b)
    {
        var g = new Grid(); g.ColumnDefinitions.Add(new ColumnDefinition()); g.ColumnDefinitions.Add(new ColumnDefinition()); g.Children.Add(a); Grid.SetColumn(b, 1); g.Children.Add(b); return g;
    }
    static Button Action(string title, System.Action action)
    {
        var b = new Button { Content = title, HorizontalContentAlignment = HorizontalAlignment.Left }; b.Click += (_, _) => action(); return b;
    }
    FrameworkElement ChainPanel(string title, ListBox list, bool isBefore)
    {
        var row = new FrameworkElementFactory(typeof(CheckBox));
        row.SetBinding(System.Windows.Controls.Primitives.ToggleButton.IsCheckedProperty, new System.Windows.Data.Binding(nameof(Hop.Enabled)) { Mode = System.Windows.Data.BindingMode.TwoWay });
        row.SetBinding(ContentControl.ContentProperty, new System.Windows.Data.Binding());
        void HopChanged(object sender, RoutedEventArgs args)
        {
            if (sender is CheckBox box && box.DataContext is Hop hop) list.SelectedItem = hop;
            Dispatcher.BeginInvoke(new System.Action(() => { UpdateFlow(); RenderXrays(); }));
        }
        row.AddHandler(System.Windows.Controls.Primitives.ToggleButton.CheckedEvent, new RoutedEventHandler(HopChanged));
        row.AddHandler(System.Windows.Controls.Primitives.ToggleButton.UncheckedEvent, new RoutedEventHandler(HopChanged));
        list.ItemTemplate = new DataTemplate { VisualTree = row };
        var p = new StackPanel { Margin = new Thickness(0, 12, 12, 0) }; p.Children.Add(new TextBlock { Text = title, Foreground=AppTheme.Text,FontWeight = FontWeights.SemiBold }); list.Height = 88; list.Margin = new Thickness(0, 8, 0, 0); p.Children.Add(list);
        var bar = new WrapPanel();
        bar.Children.Add(Action("Add", () => EditHop(isBefore, null)));
        bar.Children.Add(Action("Edit", () => { if (list.SelectedItem is Hop h) EditHop(isBefore, h); }));
        bar.Children.Add(Action("Remove", () => { if (current == null || list.SelectedItem is not Hop h) return; (isBefore ? current.Before : current.After).Remove(h); Try(() => Vault.Remove(h.Id)); RefreshChains(); }));
        bar.Children.Add(Action("↑", () => { if (current == null || list.SelectedItem is not Hop h) return; var items = isBefore ? current.Before : current.After; int i = items.IndexOf(h); if (i > 0) { items.RemoveAt(i); items.Insert(i - 1, h); RefreshChains(); list.SelectedItem = h; } }));
        p.Children.Add(bar); return p;
    }
    void EditHop(bool isBefore, Hop? h)
    {
        if (current == null) return;
        if (h?.Type == "xray")
        {
            var saved = current.Xrays.FirstOrDefault(x => x.Id == h.XrayId);
            if (saved != null) EditSavedXray(saved);
            return;
        }
        var dialog = new HopDialog(h ?? new Hop { Host = isBefore ? "127.0.0.1" : "" }) { Owner = this };
        if (dialog.ShowDialog() == true)
        {
            if (h == null) (isBefore ? current.Before : current.After).Add(dialog.Value);
            else { h.Type = dialog.Value.Type; h.Host = dialog.Value.Host; h.Port = dialog.Value.Port; h.Username = dialog.Value.Username; h.Password = dialog.Value.Password; }
            RefreshChains();
        }
    }
    void RefreshChains()
    {
        if (current != null)
            foreach (var hop in current.Before.Concat(current.After).Where(h => h.Type == "xray"))
                hop.XrayName = current.Xrays.FirstOrDefault(x => x.Id == hop.XrayId)?.Name ?? "Missing configuration";
        before.ItemsSource = null; after.ItemsSource = null; before.ItemsSource = current?.Before; after.ItemsSource = current?.After; UpdateFlow();
        RenderXrays();
    }
    void UpdateFlow()
    {
        if (current == null) return;
        var parts = new List<string> { mode.SelectedIndex == 1 ? "System traffic" : $"Apps · 127.0.0.1:{listen.Text}" };
        // Tunnel entry hops establish the VPN transport; exit hops are application destinations inside that transport.
        parts.AddRange(current.Before.Where(x => x.Enabled).Select(x => x.Type == "xray" ? x.XrayName : $"{x.Host}:{x.Port}")); parts.Add("VPN");
        if (current.XrayExit != null) parts.Add("Xray");
        parts.AddRange(current.After.Where(x => x.Enabled).Select(x => x.Type == "xray" ? x.XrayName : $"{x.Host}:{x.Port}")); parts.Add("Destination"); flow.Text = string.Join("  →  ", parts);
        xrayLabel.Text = $"{current.Xrays.Count} saved Xray configurations";
        explanation.Text = mode.SelectedIndex == 1 ? "VPN mode needs administrator access. IPv4 TCP + DNS through the chain; IPv6 is rejected. With after-VPN hops, general UDP is unsupported and rejected. Disconnect restores owned routes. No persistent kill switch." : $"Proxy mode: set your app's SOCKS5 or HTTP proxy to 127.0.0.1:{listen.Text}. Use remote DNS (socks5h). Other apps keep their normal connection. After-VPN chains support TCP only.";
    }
    void ConfigureXray()
    {
        if (current == null) return;
        var dialog = new XrayExitDialog(null) { Owner = this };
        if (dialog.ShowDialog() == true) AddXray(dialog.Value!, dialog.SavedName,true);
    }
    void ConfigureXrayText(string initial)
    {
        if (current == null) return;
        var dialog = new XrayTextDialog(initial) { Owner = this };
        if (dialog.ShowDialog() != true) return;
        var enginePath = Path.Combine(AppContext.BaseDirectory, "engine", "wayline-engine.exe");
        var info = new ProcessStartInfo(enginePath, "--import-xray") { RedirectStandardInput = true, RedirectStandardOutput = true, RedirectStandardError = true, UseShellExecute = false, CreateNoWindow = true };
        using var child = Process.Start(info) ?? throw new InvalidOperationException("Could not start the Xray importer.");
        var output = child.StandardOutput.ReadToEndAsync();
        var errors = child.StandardError.ReadToEndAsync();
        child.StandardInput.Write(dialog.Value);
        child.StandardInput.Close();
        if (!child.WaitForExit(10000)) { child.Kill(); throw new InvalidOperationException("Xray import timed out."); }
        if (child.ExitCode != 0) throw new InvalidOperationException(errors.GetAwaiter().GetResult());
        Directory.CreateDirectory(Store.DirectoryPath);
        var configPath = Path.Combine(Store.DirectoryPath, "xray-" + Guid.NewGuid().ToString("N") + ".json");
        File.WriteAllText(configPath, output.GetAwaiter().GetResult());
        var picker = new XrayExitDialog(new XrayExit(current.Xrays.LastOrDefault()?.Exit.Executable ?? "", configPath, "")) { Owner = this };
        try{if (picker.ShowDialog() == true) AddXray(picker.Value!, picker.SavedName, true);}finally{File.Delete(configPath);}
    }
    void AddXray(XrayExit value, string name, bool owned = false)
    {
        if (current == null) return;
        var saved = new SavedXray { Name = name, Exit = value, OwnedFile = owned };
        current.Xrays.Add(saved);
        current.After.Add(new Hop { Type = "xray", XrayId = saved.Id });
        Store.Save(data);
        RefreshChains();
    }
    void DisableXrays()
    {
        if (current == null) return;
        foreach (var hop in current.Before.Concat(current.After).Where(h => h.Type == "xray")) hop.Enabled = false;
        Try(() => Store.Save(data));
        RefreshChains();
    }
    void EditSavedXray(SavedXray saved)
    {
        var dialog = new XrayExitDialog(saved.Exit, saved.Name) { Owner = this };
        if (dialog.ShowDialog() == true)
        {
            saved.OwnedFile = true;
            saved.Exit = dialog.Value!;
            saved.Name = dialog.SavedName;
            Store.Save(data);
            RefreshChains();
        }
    }
    void RenderXrays()
    {
        xrayLibrary.Children.Clear();
        if (current == null) return;
        foreach (var saved in current.Xrays)
        {
            var panel = new StackPanel { Margin = new Thickness(0, 10, 0, 0) };
            var toggle = new CheckBox { FontSize=18,FontWeight=FontWeights.SemiBold,Content = saved.Name, IsChecked = current.Before.Concat(current.After).Any(h => h.XrayId == saved.Id && h.Enabled) };
            toggle.Click += (_, _) => Try(() =>
            {
                var references = current.Before.Concat(current.After).Where(h => h.XrayId == saved.Id).ToList();
                if (references.Count == 0 && toggle.IsChecked == true) current.After.Add(new Hop { Type = "xray", XrayId = saved.Id });
                foreach (var hop in references) hop.Enabled = toggle.IsChecked == true;
                Store.Save(data);
                RefreshChains();
            });
            panel.Children.Add(toggle);
            var positions=current.Before.Select((h,i)=>(h,i,side:"Entry")).Concat(current.After.Select((h,i)=>(h,i,side:"Exit"))).Where(x=>x.h.XrayId==saved.Id).Select(x=>$"{x.side} #{x.i+1} · {(x.h.Enabled?"On":"Off")}");
            panel.Children.Add(new TextBlock {Text=string.Join("   ",positions.DefaultIfEmpty("Saved · not in chain")),Foreground=AppTheme.Muted,Margin=new Thickness(22,6,0,8)});
            var actions = new WrapPanel();
            actions.Children.Add(Action("Settings", () => EditSavedXray(saved)));
            actions.Children.Add(Action("Before hop", () => { current.Before.Add(new Hop { Type = "xray", XrayId = saved.Id }); Try(() => Store.Save(data)); RefreshChains(); }));
            actions.Children.Add(Action("After hop", () => { current.After.Add(new Hop { Type = "xray", XrayId = saved.Id }); Try(() => Store.Save(data)); RefreshChains(); }));
            actions.Children.Add(Action("Delete", () => Try(() =>
            {
                if (MessageBox.Show("Delete this saved Xray configuration and its chain references?", "Wayline", MessageBoxButton.YesNo) != MessageBoxResult.Yes) return;
                current.Before.RemoveAll(h => h.XrayId == saved.Id);
                current.After.RemoveAll(h => h.XrayId == saved.Id);
                current.Xrays.Remove(saved);
                Store.Save(data);
                if (saved.OwnedFile && Path.GetDirectoryName(Path.GetFullPath(saved.Exit.ConfigFile)) == Store.DirectoryPath && !current.Xrays.Any(x => x.Exit.ConfigFile == saved.Exit.ConfigFile))
                    File.Delete(saved.Exit.ConfigFile);
                RefreshChains();
            })));
            panel.Children.Add(actions);
            xrayLibrary.Children.Add(new Border {Child=panel,Background=AppTheme.Panel,BorderBrush=AppTheme.Border,BorderThickness=new Thickness(1),CornerRadius=new CornerRadius(14),Padding=new Thickness(16),Margin=new Thickness(0,8,0,4)});
        }
    }
    void LoadProfile(Profile p)
    {
        loading = true; current = p; name.Text = p.Name; gateway.Text = p.Gateway; port.Text = p.Port.ToString(); user.Text = p.Username; realm.Text = p.Realm; cert.Text = p.CertificateSha256; listen.Text = p.ListenPort.ToString(); dns.Text = string.Join(", ", p.Dns); remember.IsChecked = p.RememberPassword; password.Password = p.RememberPassword ? ReadSavedCredential(p.Id) : "";
        foreach (var h in p.Before.Concat(p.After)) h.Password = h.RememberPassword ? ReadSavedCredential(h.Id) : "";
        reconnect.IsChecked = p.AutoReconnect;appFilter.SelectedIndex=p.AppFilterMode=="include"?1:p.AppFilterMode=="exclude"?2:0;appFilterSummary.Text=$"{p.AppFilterApps.Count} apps selected";
        mode.SelectedIndex = p.Mode == "vpn" ? 1 : 0; RefreshChains(); loading = false;
    }
    void SaveCurrent()
    {
        if (loading || current == null) return;
        if (string.IsNullOrWhiteSpace(name.Text)) throw new InvalidOperationException("Enter a profile name.");
        if (!int.TryParse(port.Text, out int vpnPort) || vpnPort < 1 || vpnPort > 65535 || !int.TryParse(listen.Text, out int proxyPort) || proxyPort < 1 || proxyPort > 65535) throw new InvalidOperationException("Ports must be between 1 and 65535.");
        if(current.Gateway!=gateway.Text.Trim()||current.Port!=vpnPort)current.TrustedCertificates.Clear();
        bool previouslyRemembered = current.RememberPassword;
        current.AutoReconnect = reconnect.IsChecked == true;current.AppFilterMode=appFilter.SelectedIndex==1?"include":appFilter.SelectedIndex==2?"exclude":"all";
        current.Name = name.Text.Trim(); current.Gateway = gateway.Text.Trim(); current.Port = vpnPort; current.Username = user.Text.Trim(); current.Realm = realm.Text.Trim(); current.CertificateSha256 = cert.Text.Trim(); current.Mode = mode.SelectedIndex == 1 ? "vpn" : "proxy"; current.ListenPort = proxyPort; current.Dns = dns.Text.Split(new[] { ',', ' ', ';' }, StringSplitOptions.RemoveEmptyEntries).ToList(); current.RememberPassword = remember.IsChecked == true;
        if (current.RememberPassword) Vault.Set(current.Id, password.Password); else if (previouslyRemembered) Vault.Remove(current.Id);
        foreach (var h in current.Before.Concat(current.After)) { if (h.Password.Length > 0) Vault.Set(h.Id, h.Password); else if (h.RememberPassword) Vault.Remove(h.Id); h.RememberPassword = h.Password.Length > 0; }
        Store.Save(data); profiles.Items.Refresh();
    }
    string ReadSavedCredential(string id)
    {
        try { return Vault.Get(id); }
        catch (System.ComponentModel.Win32Exception e) { Log("Saved credentials are unavailable in this Windows session. Enter them again, or launch Wayline normally under your desktop account. " + e.Message); return ""; }
    }
    void NewProfile() { if(current!=null)Try(SaveCurrent);var p = new Profile(); data.Add(p);Store.Save(data); profiles.Items.Refresh(); profiles.SelectedItem = p; }
    void ChooseApps(){if(current==null)return;var picker=new AppPickerDialog(current.AppFilterApps){Owner=this};if(picker.ShowDialog()==true){current.AppFilterApps=picker.Paths;Store.Save(data);appFilterSummary.Text=$"{picker.Paths.Count} apps selected";}}
    void RenameProfile()
    {
        if(current==null)return;var dialog=new TextPrompt("Rename VPN profile",name.Text){Owner=this};if(dialog.ShowDialog()!=true)return;current.Name=dialog.Value;name.Text=dialog.Value;Store.Save(data);profiles.Items.Refresh();
    }
    void ManageCertificates()
    {
        if(current==null)return;var dialog=new Window {Title="Trusted fingerprints",Width=620,Height=340,Owner=this,WindowStartupLocation=WindowStartupLocation.CenterOwner};var panel=new StackPanel {Margin=new Thickness(20)};var list=new ListBox {ItemsSource=current.TrustedCertificates,Height=180};panel.Children.Add(new TextBlock {Text="Remembered only for this VPN profile",Margin=new Thickness(0,0,0,12)});panel.Children.Add(list);panel.Children.Add(Action("Remove selected",()=>{if(list.SelectedItem is string value){current.TrustedCertificates.Remove(value);Store.Save(data);list.Items.Refresh();}}));dialog.Content=panel;dialog.ShowDialog();
    }
    void DeleteProfile()
    {
        if (current == null || MessageBox.Show($"Delete {current.Name} and its saved credentials?", "Wayline", MessageBoxButton.YesNo) != MessageBoxResult.Yes) return;
        Try(() => { if (current.RememberPassword) Vault.Remove(current.Id); foreach (var h in current.Before.Concat(current.After)) if (h.RememberPassword) Vault.Remove(h.Id); data.Remove(current); if (data.Count == 0) data.Add(new Profile()); Store.Save(data); profiles.Items.Refresh(); profiles.SelectedIndex = 0; });
    }
    void Import()
    {
        var dialog = new OpenFileDialog { Filter = "Wayline profile (*.json)|*.json" };
        if (dialog.ShowDialog() != true) return;
        Try(() => { var p = JsonSerializer.Deserialize<Profile>(File.ReadAllText(dialog.FileName), Store.Options) ?? throw new InvalidOperationException("Empty profile."); p.Id = Guid.NewGuid().ToString("N"); p.RememberPassword = false;p.AppFilterMode="all";p.AppFilterApps.Clear(); p.XrayExit = null; p.Xrays.Clear(); p.Before.RemoveAll(h => h.Type == "xray"); p.After.RemoveAll(h => h.Type == "xray"); foreach (var h in p.Before.Concat(p.After)) { h.Id = Guid.NewGuid().ToString("N"); h.RememberPassword = false; } data.Add(p); Store.Save(data); profiles.Items.Refresh(); profiles.SelectedItem = p; });
    }
    void Export()
    {
        Try(() => { SaveCurrent(); if (current == null) return; var dialog = new SaveFileDialog { Filter = "Wayline profile (*.json)|*.json", FileName = "connection.json" }; if (dialog.ShowDialog() != true) return; var copy = JsonSerializer.Deserialize<Profile>(JsonSerializer.Serialize(current, Store.Options), Store.Options)!; copy.RememberPassword = false;copy.AppFilterMode="all";copy.AppFilterApps.Clear(); copy.XrayExit = null; copy.Xrays.Clear(); copy.Before.RemoveAll(h => h.Type == "xray"); copy.After.RemoveAll(h => h.Type == "xray"); if (MessageBox.Show("Remove usernames from the exported profile?", "Export profile", MessageBoxButton.YesNo) == MessageBoxResult.Yes) { copy.Username = ""; foreach (var h in copy.Before.Concat(copy.After)) h.Username = ""; } File.WriteAllText(dialog.FileName, JsonSerializer.Serialize(copy, Store.Options)); });
    }
    static void Try(System.Action action) { try { action(); } catch (Exception e) { MessageBox.Show(e.Message, "Wayline", MessageBoxButton.OK, MessageBoxImage.Error); } }
    void Log(string text)
    {
        foreach (var secret in new[] { password.Password }.Concat(current?.Before.Concat(current.After).Select(h => h.Password) ?? [])) if (!string.IsNullOrEmpty(secret)) text = text.Replace(secret, "[redacted]", StringComparison.Ordinal);
        logs.AppendText($"{DateTime.Now:HH:mm:ss}  {text}\n"); if (logs.Text.Length > 100000) logs.Text = logs.Text[^70000..]; logs.ScrollToEnd();
    }
    async Task Connect()
    {
        if (engine != null) return;
        try
        {
            SaveCurrent(); var p = current!;if(string.IsNullOrWhiteSpace(p.Gateway)||string.IsNullOrWhiteSpace(p.Username))throw new InvalidOperationException("Enter a gateway and username.");
            if (p.Mode == "vpn" && !new WindowsPrincipal(WindowsIdentity.GetCurrent()).IsInRole(WindowsBuiltInRole.Administrator))
            {
                if (MessageBox.Show("VPN mode needs administrator access. Reopen Wayline as administrator? Unsaved passwords must be entered again.", "VPN mode", MessageBoxButton.YesNo) == MessageBoxResult.Yes) { Process.Start(new ProcessStartInfo(Environment.ProcessPath!) { UseShellExecute = true, Verb = "runas", WorkingDirectory = AppContext.BaseDirectory }); Close(); }
                return;
            }
            if (password.Password.Length == 0) throw new InvalidOperationException("Enter your VPN password first.");
            var path = Path.Combine(AppContext.BaseDirectory, "engine", "wayline-engine.exe"); if (!File.Exists(path)) throw new FileNotFoundException("The VPN engine is missing. Keep the engine folder beside Wayline.exe.");
            var psi = new ProcessStartInfo(path, "--session") { UseShellExecute = false, RedirectStandardInput = true, RedirectStandardOutput = true, RedirectStandardError = true, CreateNoWindow = true, WorkingDirectory = Path.GetDirectoryName(path)! };
            engine = Process.Start(psi) ?? throw new InvalidOperationException("Could not start the VPN engine."); var process = engine;
            editor.IsEnabled = false;chainEditor.IsEnabled=false;xrayEditor.IsEnabled=false; sidebar.IsEnabled = false; connect.IsEnabled = false; disconnect.IsEnabled = true; status.Text = "CONNECTING";
            var config = new { gateway = p.Gateway, port = p.Port, username = p.Username, password = password.Password, realm = p.Realm, certificateSha256 = onceFingerprint ?? p.CertificateSha256, trustedCertificates=p.TrustedCertificates,appFilterMode=p.AppFilterMode,appFilterApps=p.AppFilterApps, mode = p.Mode, listenPort = p.ListenPort, before = p.Before.Where(h => h.Enabled).Select(p.WireHop), after = p.After.Where(h => h.Enabled).Select(p.WireHop), dns = p.Dns, tunName = "Wayline", autoReconnect = p.AutoReconnect };
            onceFingerprint=null;await process.StandardInput.WriteLineAsync(JsonSerializer.Serialize(config)); await process.StandardInput.FlushAsync();
            var stdout = ReadOutput(process.StandardOutput, true); var stderr = ReadOutput(process.StandardError, false);
            await process.WaitForExitAsync(); await Task.WhenAll(stdout, stderr); Log($"Engine exited ({process.ExitCode}).");
        }
        catch (Exception e) { Log(e.Message); MessageBox.Show(e.Message, "Wayline", MessageBoxButton.OK, MessageBoxImage.Error); }
        finally { if (engine is { HasExited: false }) { try { engine.Kill(true); } catch { } } engine?.Dispose(); engine = null; editor.IsEnabled = true;chainEditor.IsEnabled=true;xrayEditor.IsEnabled=true; sidebar.IsEnabled = true; connect.IsEnabled = true; disconnect.IsEnabled = false; status.Text = "DISCONNECTED";if(retryCertificate){retryCertificate=false;_ = Dispatcher.BeginInvoke(new System.Action(async()=>await Connect()));} }
    }
    async Task ReadOutput(StreamReader reader, bool json)
    {
        while (await reader.ReadLineAsync() is { } line)
        {
            if (!json) { Log(line); continue; }
            try
            {
                using var doc = JsonDocument.Parse(line); var root = doc.RootElement;
                string type = root.TryGetProperty("type", out var t) ? t.GetString() ?? "" : "";
                string message = root.TryGetProperty("message", out var m) ? m.GetString() ?? "" : "";
                if (type == "certificate")
                {
                    lastFingerprint = root.TryGetProperty("sha256", out var fp) ? fp.GetString() : null;
                    Log("Certificate rejected. Verify this SHA-256 with your IT team before saving it: " + lastFingerprint);
                    if(lastFingerprint is {Length:64}) {
                        var decision=new CertificateDialog(lastFingerprint){Owner=this};
                        if(decision.ShowDialog()==true){if(decision.Remember){if(current!.TrustedCertificates.Count>=31)throw new InvalidOperationException("Remove an old fingerprint first.");if(!current.TrustedCertificates.Contains(lastFingerprint))current.TrustedCertificates.Add(lastFingerprint);Store.Save(data);}else onceFingerprint=lastFingerprint;retryCertificate=true;}
                    }
                }
                else if (type == "stats") { long incoming = root.TryGetProperty("bytesIn", out var bi) ? bi.GetInt64() : 0; long outgoing = root.TryGetProperty("bytesOut", out var bo) ? bo.GetInt64() : 0; status.Text = $"CONNECTED  ↓ {incoming / 1048576.0:F1} MB  ↑ {outgoing / 1048576.0:F1} MB"; }
                else { if (message.Length > 0) Log(message); if (type == "status" || type == "retry") status.Text = message.ToUpperInvariant(); }
            }
            catch (JsonException) { Log(line); }
        }
    }
    async Task Stop()
    {
        var process = engine; if (process == null) return; disconnect.IsEnabled = false; status.Text = "DISCONNECTING";
        try { process.StandardInput.Close(); var exit = process.WaitForExitAsync(); if (await Task.WhenAny(exit, Task.Delay(10000)) != exit && !process.HasExited) process.Kill(true); } catch (InvalidOperationException) { }
    }
    async Task CloseAfterStop() { await Stop(); Close(); }
}

public sealed class HopDialog : Window
{
    public Hop Value { get; private set; }
    public HopDialog(Hop? existing)
    {
        Value = existing ?? new Hop(); Title = existing == null ? "Add proxy hop" : "Edit proxy hop"; Width = 420; Height = 520; ResizeMode = ResizeMode.NoResize; WindowStartupLocation = WindowStartupLocation.CenterOwner; FontFamily = new FontFamily("Segoe UI"); FontSize = 14;
        var panel = new StackPanel { Margin = new Thickness(24) }; Content = panel;
        var type = new ComboBox { ItemsSource = new[] { "socks5", "http", "https" }, SelectedItem = Value.Type };
        var host = new TextBox { Text = Value.Host }; var port = new TextBox { Text = Value.Port.ToString() }; var username = new TextBox { Text = Value.Username }; var password = new PasswordBox { Password = Value.Password };
        void Add(string label, Control c) { panel.Children.Add(new TextBlock { Text = label, Margin = new Thickness(0, 8, 0, 5) }); c.Padding = new Thickness(8); panel.Children.Add(c); }
        Add("Protocol", type); Add("Host or proxy URL", host); Add("Port", port); Add("Username (optional)", username); Add("Password (optional)", password);
        panel.Children.Add(new TextBlock { Text = "Hop passwords are saved in Windows Credential Manager when you save or connect the profile.", TextWrapping = TextWrapping.Wrap, FontSize = 12, Foreground = AppTheme.Muted, Margin = new Thickness(0, 12, 0, 0) });
        var save = new Button { Content = "Save hop", Padding = new Thickness(12), Margin = new Thickness(0, 14, 0, 0), IsDefault = true };
        save.Click += (_, _) =>
        {
            string target = host.Text.Trim();
            string protocol = (string)type.SelectedItem;
            int.TryParse(port.Text, out int number);
            if (target.Contains("://"))
            {
                if (!Uri.TryCreate(target, UriKind.Absolute, out var uri) || uri.Scheme is not ("socks5" or "http" or "https") || uri.UserInfo.Length != 0 || uri.PathAndQuery is not ("" or "/") || uri.Fragment.Length != 0)
                { MessageBox.Show("Use a SOCKS5, HTTP or HTTPS URL with a host and port. Enter credentials in the fields below."); return; }
                target = uri.IdnHost.Trim('[', ']'); protocol = uri.Scheme; number = uri.Port;
            }
            if (string.IsNullOrWhiteSpace(target) || number is < 1 or > 65535) { MessageBox.Show("Enter a host and a port from 1 to 65535."); return; }
            Value = new Hop { Id = Value.Id, Type = protocol, Host = target, Port = number, Username = username.Text, Password = password.Password };
            DialogResult = true;
        };
        panel.Children.Add(save);
    }
}
