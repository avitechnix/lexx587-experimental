import Toybox.Application;
import Toybox.Lang;
import Toybox.WatchUi;

class TeslaApp extends Application.AppBase {

    private var _car as Car?;

    function initialize() {
        AppBase.initialize();
    }

    function onStart(state as Dictionary?) as Void {
    }

    function onStop(state as Dictionary?) as Void {
    }

    function getInitialView() as [WatchUi.Views] or [WatchUi.Views, WatchUi.InputDelegates] {
        _car = new Car();
        var car = _car as Car;
        car.refresh();
        return [new StatusView(car), new StatusDelegate(car)];
    }

    (:glance)
    function getGlanceView() as [WatchUi.GlanceView] or [WatchUi.GlanceView, WatchUi.GlanceViewDelegate] or Null {
        return [new TeslaGlanceView()];
    }

    // The token or VIN was edited in the Garmin Connect app.
    function onSettingsChanged() as Void {
        Application.Storage.deleteValue("vin");
        if (_car != null) {
            (_car as Car).refresh();
        }
    }
}
