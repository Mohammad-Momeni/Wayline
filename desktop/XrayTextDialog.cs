using System.Windows;
using System.Windows.Controls;

namespace Wayline;

public sealed class XrayTextDialog : Window
{
    public string Value { get; private set; } = "";
    public XrayTextDialog(string initial)
    {
        Title = "Import Xray";
        Width = 580; Height = 340; MinWidth = 340; MinHeight = 260;
        WindowStartupLocation = WindowStartupLocation.CenterOwner;
        var panel = new DockPanel { Margin = new Thickness(18) };
        var label = new TextBlock { Text = "Paste Xray JSON or a vmess://, vless://, trojan:// or ss:// share link.", TextWrapping = TextWrapping.Wrap, Margin = new Thickness(0, 0, 0, 12) };
        DockPanel.SetDock(label, Dock.Top); panel.Children.Add(label);
        var actions = new StackPanel { Orientation = Orientation.Horizontal, Margin = new Thickness(0, 12, 0, 0) };
        var paste = new Button { Content = "Paste clipboard", Padding = new Thickness(10) };
        var import = new Button { Content = "Import", Padding = new Thickness(10), Margin = new Thickness(8, 0, 0, 0) };
        actions.Children.Add(paste); actions.Children.Add(import);
        DockPanel.SetDock(actions, Dock.Bottom); panel.Children.Add(actions);
        var text = new TextBox { Text = initial, AcceptsReturn = true, TextWrapping = TextWrapping.Wrap, VerticalScrollBarVisibility = ScrollBarVisibility.Auto, MaxLength = 1 << 20 };
        paste.Click += (_, _) => { if (Clipboard.ContainsText()) text.Text = Clipboard.GetText(); };
        import.Click += (_, _) => { if (string.IsNullOrWhiteSpace(text.Text)) return; Value = text.Text; DialogResult = true; };
        panel.Children.Add(text); Content = panel;
    }
}
