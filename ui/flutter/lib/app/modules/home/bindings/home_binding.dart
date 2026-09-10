import 'package:get/get.dart';

import '../../float_window/views/float_window.dart';
import '../controllers/home_controller.dart';

class HomeBinding extends Bindings {
  @override
  void dependencies() {
    Get.lazyPut<HomeController>(
      () => HomeController(),
      fenix: true,
    );
    Get.lazyPut<FloatWindowController>(
      () => FloatWindowController(),
      fenix: true,
    );
  }
}
