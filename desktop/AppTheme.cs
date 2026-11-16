using System.IO;
using System.Windows;
using System.Windows.Controls;
using System.Windows.Media;
using System.Windows.Markup;

namespace Wayline;

public static class AppTheme
{
    public static bool Dark { get; private set; } = true;
    public static Brush Background => Color(Dark ? "#0C1420" : "#F1F5F8");
    public static Brush Panel => Color(Dark ? "#172536" : "#FFFFFF");
    public static Brush Input => Color(Dark ? "#121E2D" : "#FFFFFF");
    public static Brush Text => Color(Dark ? "#E4EDF7" : "#1C293E");
    public static Brush Muted => Color(Dark ? "#A8BACD" : "#53667A");
    public static Brush Border => Color(Dark ? "#41546B" : "#A7B6C6");
    public static Brush Accent => Color(Dark ? "#70E4C3" : "#116D66");
    public static Brush Highlight => Color(Dark ? "#183D40" : "#E7F6F1");
    static Brush Color(string value) => new SolidColorBrush((System.Windows.Media.Color)ColorConverter.ConvertFromString(value));
    public static void Load()
    {
        var path = Path.Combine(Store.DirectoryPath, "theme.txt");
        if (File.Exists(path)) Dark = File.ReadAllText(path).Trim() != "light";
    }
    public static void Toggle()
    {
        Dark = !Dark;
        Directory.CreateDirectory(Store.DirectoryPath);
        File.WriteAllText(Path.Combine(Store.DirectoryPath, "theme.txt"), Dark ? "dark" : "light");
    }
    public static ResourceDictionary Resources()
    {
        var resources = new ResourceDictionary();
        foreach (var type in new[] { typeof(Window), typeof(TextBox), typeof(PasswordBox), typeof(Button), typeof(CheckBox), typeof(ComboBox), typeof(ComboBoxItem), typeof(ListBox), typeof(ListBoxItem), typeof(TabControl), typeof(TabItem) })
        {
            var style = new Style(type);
            style.Setters.Add(new Setter(Control.ForegroundProperty, Text));
            style.Setters.Add(new Setter(Control.BackgroundProperty, type == typeof(Window) ? Background : Input));
            style.Setters.Add(new Setter(Control.BorderBrushProperty, Border));
            resources.Add(type, style);
        }
        var tab=(Style)resources[typeof(TabItem)];
        tab.Setters.Add(new Setter(Control.TemplateProperty,(ControlTemplate)XamlReader.Parse("""
            <ControlTemplate xmlns="http://schemas.microsoft.com/winfx/2006/xaml/presentation" xmlns:x="http://schemas.microsoft.com/winfx/2006/xaml" TargetType="TabItem">
              <Border x:Name="frame" Background="{TemplateBinding Background}" BorderBrush="{TemplateBinding BorderBrush}" BorderThickness="1" CornerRadius="10" Padding="{TemplateBinding Padding}" Margin="0,0,7,10">
                <ContentPresenter ContentSource="Header" HorizontalAlignment="Center" VerticalAlignment="Center"/>
              </Border>
              <ControlTemplate.Triggers>
                <Trigger Property="IsSelected" Value="True"><Setter TargetName="frame" Property="Opacity" Value="1"/><Setter TargetName="frame" Property="BorderBrush" Value="#5B9F92"/></Trigger>
                <Trigger Property="IsSelected" Value="False"><Setter TargetName="frame" Property="Opacity" Value="0.65"/></Trigger>
                <Trigger Property="IsMouseOver" Value="True"><Setter TargetName="frame" Property="Opacity" Value="1"/></Trigger>
              </ControlTemplate.Triggers>
            </ControlTemplate>
            """)));
        var combo = (Style)resources[typeof(ComboBox)];
        combo.Setters.Add(new Setter(Control.TemplateProperty, (ControlTemplate)XamlReader.Parse("""
            <ControlTemplate xmlns="http://schemas.microsoft.com/winfx/2006/xaml/presentation"
                             xmlns:x="http://schemas.microsoft.com/winfx/2006/xaml"
                             TargetType="ComboBox">
                <Grid>
                    <ToggleButton Focusable="False"
                                  IsChecked="{Binding IsDropDownOpen, RelativeSource={RelativeSource TemplatedParent}, Mode=TwoWay}">
                        <ToggleButton.Template>
                            <ControlTemplate TargetType="ToggleButton">
                                <Border Background="{Binding Background, RelativeSource={RelativeSource AncestorType=ComboBox}}"
                                        BorderBrush="{Binding BorderBrush, RelativeSource={RelativeSource AncestorType=ComboBox}}"
                                        BorderThickness="1" CornerRadius="4"/>
                            </ControlTemplate>
                        </ToggleButton.Template>
                    </ToggleButton>
                    <ContentPresenter Margin="9,7,26,7" IsHitTestVisible="False"
                                      Content="{TemplateBinding SelectionBoxItem}"
                                      ContentTemplate="{TemplateBinding SelectionBoxItemTemplate}"/>
                    <TextBlock Text="▾" HorizontalAlignment="Right" VerticalAlignment="Center" Margin="0,0,9,0" IsHitTestVisible="False"/>
                    <Popup IsOpen="{TemplateBinding IsDropDownOpen}" Placement="Bottom" AllowsTransparency="True" Focusable="False">
                        <Border Background="{TemplateBinding Background}" BorderBrush="{TemplateBinding BorderBrush}"
                                BorderThickness="1" MinWidth="{Binding ActualWidth, RelativeSource={RelativeSource TemplatedParent}}">
                            <ScrollViewer MaxHeight="260"><ItemsPresenter/></ScrollViewer>
                        </Border>
                    </Popup>
                </Grid>
            </ControlTemplate>
            """)));
        var button = (Style)resources[typeof(Button)];
        button.Setters.Add(new Setter(Control.TemplateProperty, (ControlTemplate)XamlReader.Parse("""
            <ControlTemplate xmlns="http://schemas.microsoft.com/winfx/2006/xaml/presentation"
                             xmlns:x="http://schemas.microsoft.com/winfx/2006/xaml" TargetType="Button">
                <Border x:Name="frame" Background="{TemplateBinding Background}" BorderBrush="{TemplateBinding BorderBrush}"
                        BorderThickness="1" CornerRadius="12" Padding="{TemplateBinding Padding}">
                    <ContentPresenter HorizontalAlignment="{TemplateBinding HorizontalContentAlignment}" VerticalAlignment="Center"/>
                </Border>
                <ControlTemplate.Triggers>
                    <Trigger Property="IsMouseOver" Value="True"><Setter TargetName="frame" Property="Opacity" Value="0.8"/></Trigger>
                    <Trigger Property="IsEnabled" Value="False"><Setter TargetName="frame" Property="Opacity" Value="0.4"/></Trigger>
                </ControlTemplate.Triggers>
            </ControlTemplate>
            """)));
        var check = (Style)resources[typeof(CheckBox)];
        check.Setters.Add(new Setter(Control.TemplateProperty, (ControlTemplate)XamlReader.Parse("""
            <ControlTemplate xmlns="http://schemas.microsoft.com/winfx/2006/xaml/presentation"
                             xmlns:x="http://schemas.microsoft.com/winfx/2006/xaml" TargetType="CheckBox">
                <StackPanel Orientation="Horizontal">
                    <Border Width="15" Height="15" Background="{TemplateBinding Background}"
                            BorderBrush="{TemplateBinding BorderBrush}" BorderThickness="1" CornerRadius="2" VerticalAlignment="Center">
                        <Path x:Name="tick" Data="M2,6 L5,9 L11,2" Stroke="{TemplateBinding Foreground}"
                              StrokeThickness="2" Visibility="Collapsed" Margin="1"/>
                    </Border>
                    <ContentPresenter Margin="6,0,0,0" VerticalAlignment="Center"/>
                </StackPanel>
                <ControlTemplate.Triggers>
                    <Trigger Property="IsChecked" Value="True"><Setter TargetName="tick" Property="Visibility" Value="Visible"/></Trigger>
                    <Trigger Property="IsEnabled" Value="False"><Setter Property="Opacity" Value="0.4"/></Trigger>
                </ControlTemplate.Triggers>
            </ControlTemplate>
            """)));
        resources[SystemColors.WindowBrushKey] = Input;
        resources[SystemColors.WindowTextBrushKey] = Text;
        resources[SystemColors.ControlBrushKey] = Panel;
        resources[SystemColors.ControlTextBrushKey] = Text;
        return resources;
    }
}
